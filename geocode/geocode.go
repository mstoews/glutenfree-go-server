// Package geocode turns Japanese street addresses into coordinates.
//
// The default implementation uses the Geospatial Information Authority of Japan
// (GSI / 国土地理院) address-search endpoint: it is free, needs no API key, and
// resolves Japanese addresses well. The Geocoder interface exists so a paid
// provider (e.g. Google Maps Geocoding) can be swapped in without touching
// callers.
package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrNotFound means the provider returned no match for the address.
var ErrNotFound = errors.New("address not found")

// Result is a resolved address.
type Result struct {
	Lat float64
	Lng float64
	// MatchedAddress is the normalised address the provider actually matched.
	MatchedAddress string
	// Precise is false when the provider could only place the address at a
	// ward/city centroid (e.g. "東京都渋谷区"). Callers should not treat an
	// imprecise result as the store's location.
	Precise bool
}

// Geocoder resolves an address to coordinates.
type Geocoder interface {
	Geocode(ctx context.Context, address string) (Result, error)
}

const (
	gsiBaseURL = "https://msearch.gsi.go.jp/address-search/AddressSearch"
	// gsiMinInterval throttles calls: GSI is a free public service and we may
	// geocode a whole imported batch at once.
	gsiMinInterval = 250 * time.Millisecond
)

// GSI geocodes via the GSI address-search endpoint. Safe for concurrent use;
// calls are serialised to respect the throttle.
type GSI struct {
	client      *http.Client
	baseURL     string
	minInterval time.Duration

	mu   sync.Mutex
	last time.Time
}

// NewGSI returns a GSI geocoder with sensible timeouts and throttling.
func NewGSI() *GSI {
	return &GSI{
		client:      &http.Client{Timeout: 15 * time.Second},
		baseURL:     gsiBaseURL,
		minInterval: gsiMinInterval,
	}
}

// gsiFeature mirrors the GeoJSON-ish response GSI returns.
type gsiFeature struct {
	Geometry struct {
		Coordinates []float64 `json:"coordinates"` // [lng, lat]
	} `json:"geometry"`
	Properties struct {
		Title string `json:"title"`
	} `json:"properties"`
}

// throttle blocks until minInterval has elapsed since the previous call.
func (g *GSI) throttle(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if wait := g.minInterval - time.Since(g.last); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	g.last = time.Now()
	return nil
}

// postalPrefix matches a leading Japanese postal code, with or without 〒 and
// with or without the hyphen (e.g. "〒101-0021 ", "1010021 ").
var postalPrefix = regexp.MustCompile(`^\s*〒?\s*\d{3}-?\d{4}\s*`)

// Normalize prepares an address for the geocoder. GSI fails outright on an
// address that leads with a postal code ("〒101-0021 東京都…"), which is common
// in scraped data, so strip it; full-width spaces are also normalised.
func Normalize(address string) string {
	address = strings.ReplaceAll(address, "　", " ") // ideographic space
	address = postalPrefix.ReplaceAllString(address, "")
	return strings.TrimSpace(address)
}

// Geocode resolves a Japanese address. It returns ErrNotFound when the provider
// has no match; a ward-centroid match is returned with Precise=false.
func (g *GSI) Geocode(ctx context.Context, address string) (Result, error) {
	address = Normalize(address)
	if address == "" {
		return Result{}, ErrNotFound
	}
	if err := g.throttle(ctx); err != nil {
		return Result{}, err
	}

	endpoint := g.baseURL + "?q=" + url.QueryEscape(address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "gurufuri-admin/1.0 (+https://gurufuri-jp.com)")

	resp, err := g.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("geocoder returned HTTP %d", resp.StatusCode)
	}

	var features []gsiFeature
	if err := json.NewDecoder(resp.Body).Decode(&features); err != nil {
		return Result{}, fmt.Errorf("decode geocoder response: %w", err)
	}
	if len(features) == 0 {
		return Result{}, ErrNotFound
	}

	f := features[0]
	if len(f.Geometry.Coordinates) < 2 {
		return Result{}, ErrNotFound
	}
	return Result{
		Lng:            f.Geometry.Coordinates[0],
		Lat:            f.Geometry.Coordinates[1],
		MatchedAddress: f.Properties.Title,
		Precise:        preciseEnough(f.Properties.Title),
	}, nil
}

// preciseEnough reports whether a match resolved past the ward/city centroid.
// GSI answers "東京都渋谷区" when it can only place the address in the ward,
// which would drop a pin in the middle of Shibuya; anything with detail after
// the 区/市 (a chome/banchi) is specific enough to map.
func preciseEnough(title string) bool {
	for _, marker := range []string{"区", "市"} {
		if i := strings.Index(title, marker); i >= 0 {
			return strings.TrimSpace(title[i+len(marker):]) != ""
		}
	}
	return false
}

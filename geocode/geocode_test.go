package geocode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubGSI serves a canned GSI response and records the query it received.
func stubGSI(t *testing.T, body string) (*GSI, *string) {
	t.Helper()
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &GSI{client: srv.Client(), baseURL: srv.URL, minInterval: 0}, &gotQuery
}

func TestGeocode_PreciseMatch(t *testing.T) {
	g, gotQuery := stubGSI(t, `[{"geometry":{"coordinates":[139.687424,35.668423],"type":"Point"},
		"properties":{"title":"東京都渋谷区上原一丁目１番２０号"}}]`)

	res, err := g.Geocode(context.Background(), "東京都渋谷区上原1-1-20 JPビル 3F")
	if err != nil {
		t.Fatalf("Geocode: %v", err)
	}
	if res.Lat != 35.668423 || res.Lng != 139.687424 {
		t.Errorf("coords = %v,%v want 35.668423,139.687424", res.Lat, res.Lng)
	}
	if !res.Precise {
		t.Error("building-level match should be Precise")
	}
	if *gotQuery != "東京都渋谷区上原1-1-20 JPビル 3F" {
		t.Errorf("query sent = %q", *gotQuery)
	}
}

// A ward-only match must be flagged imprecise so callers don't pin a store to
// the middle of the ward.
func TestGeocode_WardCentroidIsImprecise(t *testing.T) {
	g, _ := stubGSI(t, `[{"geometry":{"coordinates":[139.697723,35.66367],"type":"Point"},
		"properties":{"title":"東京都渋谷区"}}]`)

	res, err := g.Geocode(context.Background(), "東京都渋谷区原宿 ※駅から徒歩8分")
	if err != nil {
		t.Fatalf("Geocode: %v", err)
	}
	if res.Precise {
		t.Error("ward centroid must not be Precise")
	}
	if res.MatchedAddress != "東京都渋谷区" {
		t.Errorf("MatchedAddress = %q", res.MatchedAddress)
	}
}

func TestGeocode_NoResults(t *testing.T) {
	g, _ := stubGSI(t, `[]`)
	if _, err := g.Geocode(context.Background(), "この住所は存在しません"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGeocode_EmptyAddress(t *testing.T) {
	g, _ := stubGSI(t, `[]`)
	if _, err := g.Geocode(context.Background(), "   "); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGeocode_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	g := &GSI{client: srv.Client(), baseURL: srv.URL}

	if _, err := g.Geocode(context.Background(), "東京都渋谷区上原1-1-20"); err == nil {
		t.Error("expected an error on HTTP 503")
	}
}

func TestGeocode_Throttles(t *testing.T) {
	g, _ := stubGSI(t, `[{"geometry":{"coordinates":[139.6,35.6]},"properties":{"title":"東京都渋谷区上原一丁目"}}]`)
	g.minInterval = 60 * time.Millisecond

	start := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := g.Geocode(context.Background(), "東京都渋谷区上原1-1-20"); err != nil {
			t.Fatalf("Geocode: %v", err)
		}
	}
	// First call is immediate; the next two each wait minInterval.
	if elapsed := time.Since(start); elapsed < 120*time.Millisecond {
		t.Errorf("3 calls took %v, want >= 120ms (throttle not applied)", elapsed)
	}
}

// Scraped addresses commonly lead with a postal code, which GSI cannot parse —
// stripping it is what lifts the hit rate.
func TestNormalize(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"〒101-0021 東京都千代田区外神田5-3-3", "東京都千代田区外神田5-3-3"},
		{"〒1010021 東京都千代田区外神田5-3-3", "東京都千代田区外神田5-3-3"},
		{"101-0021 東京都千代田区外神田5-3-3", "東京都千代田区外神田5-3-3"},
		{"〒106-0032　東京都港区六本木7-8-5 2階", "東京都港区六本木7-8-5 2階"},   // ideographic space
		{"東京都渋谷区上原1-1-20 JPビル 3F", "東京都渋谷区上原1-1-20 JPビル 3F"}, // untouched
		{"  東京都渋谷区上原1-1-20  ", "東京都渋谷区上原1-1-20"},
		{"", ""},
		// A house number must not be mistaken for a postal code.
		{"東京都中央区銀座1-2-3", "東京都中央区銀座1-2-3"},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGeocode_StripsPostalCodeBeforeQuerying(t *testing.T) {
	g, gotQuery := stubGSI(t, `[{"geometry":{"coordinates":[139.77217,35.70486]},
		"properties":{"title":"東京都千代田区外神田五丁目３番３号"}}]`)

	if _, err := g.Geocode(context.Background(), "〒101-0021 東京都千代田区外神田5-3-3"); err != nil {
		t.Fatalf("Geocode: %v", err)
	}
	if *gotQuery != "東京都千代田区外神田5-3-3" {
		t.Errorf("query sent = %q, want the postal code stripped", *gotQuery)
	}
}

func TestPreciseEnough(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{"東京都渋谷区上原一丁目１番２０号", true},
		{"東京都渋谷区神泉町１番２０号", true},
		{"東京都渋谷区神泉町", true}, // town level is still specific enough to map
		{"東京都渋谷区", false},   // ward centroid
		{"東京都八王子市", false},  // city centroid
		{"東京都八王子市明神町", true},
		{"東京都", false}, // prefecture only
		{"", false},
	}
	for _, tt := range tests {
		if got := preciseEnough(tt.title); got != tt.want {
			t.Errorf("preciseEnough(%q) = %v, want %v", tt.title, got, tt.want)
		}
	}
}

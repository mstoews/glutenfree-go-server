package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Candidate struct {
	Name      string   `json:"name"`
	Address   string   `json:"address"`
	Phone     string   `json:"phone"`
	SourceURL string   `json:"source_url"`
	Cuisine   string   `json:"cuisine"`
	Evidence  string   `json:"evidence"`
	Images    []string `json:"images"`
	Menus     []string `json:"menus"`
	MenuText  string   `json:"menu_text"`
	Warnings  []string `json:"warnings"`
}
type Provider interface {
	Search(context.Context, string, int) ([]string, error)
	Capture(context.Context, string) (Candidate, error)
}
type Service struct {
	Key    string
	Client *http.Client
}

func New(key string) *Service { return &Service{Key: key, Client: NewClient()} }
func (s *Service) Search(ctx context.Context, query string, limit int) ([]string, error) {
	if s.Key == "" {
		return nil, errors.New("BRAVE_SEARCH_API_KEY is not configured; paste restaurant URLs instead")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.search.brave.com/res/v1/web/search?q="+url.QueryEscape(query)+fmt.Sprintf("&count=%d&country=JP", limit), nil)
	req.Header.Set("X-Subscription-Token", s.Key)
	// Never forward the search credential through provider redirects.
	searchClient := *s.Client
	searchClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := searchClient.Do(req)
	if err != nil {
		return nil, errors.New("search provider unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("search provider returned HTTP %d", res.StatusCode)
	}
	var body struct {
		Web struct {
			Results []struct {
				URL string `json:"url"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&body); err != nil {
		return nil, errors.New("invalid search response")
	}
	urls := []string{}
	seen := map[string]bool{}
	for _, r := range body.Web.Results {
		if u, err := URL(r.URL); err == nil && !seen[u.String()] {
			seen[u.String()] = true
			urls = append(urls, u.String())
			if len(urls) == limit {
				break
			}
		}
	}
	return urls, nil
}
func (s *Service) Capture(ctx context.Context, raw string) (Candidate, error) {
	data, final, kind, err := fetch(ctx, s.Client, raw)
	if err != nil {
		return Candidate{}, err
	}
	if !strings.Contains(kind, "html") {
		return Candidate{}, errors.New("restaurant source must be an HTML page")
	}
	c, err := Extract(data, final)
	if err != nil {
		return c, err
	}
	// Follow at most one menu page. PDFs remain links; no fabricated OCR or prices.
	if len(c.Menus) > 0 {
		content, _, mime, err := fetch(ctx, s.Client, c.Menus[0])
		if err != nil {
			c.Warnings = append(c.Warnings, "Menu capture failed; source link retained")
		} else if strings.Contains(mime, "html") {
			c.MenuText = pageText(content, 12000)
		} else {
			c.Warnings = append(c.Warnings, "Menu retained as a link; PDF/image text requires manual review")
		}
	}
	return c, nil
}

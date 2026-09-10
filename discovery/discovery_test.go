package discovery

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

const restaurantHTML = `<html><script type="application/ld+json">{"@context":"https://schema.org","@type":"Restaurant","name":"Rice Cafe","address":{"addressLocality":"Shibuya","streetAddress":"1-2-3"},"image":"/photo.jpg","hasMenu":"/menu"}</script><body>Gluten-free options available. Ask about shared equipment.<a href="/menu.pdf">Menu PDF</a></body></html>`

func TestExtract(t *testing.T) {
	c, err := Extract([]byte(restaurantHTML), "https://example.com/restaurant")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Rice Cafe" || c.Address != "Shibuya 1-2-3" || c.Images[0] != "https://example.com/photo.jpg" || len(c.Menus) != 2 || c.Evidence == "" {
		t.Fatalf("bad candidate: %+v", c)
	}
	for _, page := range []string{`<h1>Best gluten-free restaurants</h1>`, strings.ReplaceAll(restaurantHTML, "Gluten-free", "Vegan"), strings.ReplaceAll(restaurantHTML, `"name":"Rice Cafe",`, ""), restaurantHTML + restaurantHTML} {
		if _, err := Extract([]byte(page), "https://example.com"); err == nil {
			t.Fatal("accepted ambiguous or incomplete page")
		}
	}
}
func TestURLAndNetworkProtection(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://localhost:8090", "http://127.0.0.1/", "http://169.254.169.254/", "http://[::1]/", "https://user:pass@example.com", "http://100.64.0.1", "http://[::ffff:127.0.0.1]"} {
		if _, err := URL(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "::1", "fe80::1", "fc00::1", "169.254.169.254", "100.64.0.1", "::ffff:192.168.1.1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("public: %s", ip)
		}
	}
	if !publicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("blocked public IP")
	}
	if _, _, _, err := fetch(context.Background(), NewClient(), "http://localhost/"); err == nil {
		t.Fatal("allowed private DNS destination")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, body, kind string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{kind}}, Request: r}
}
func TestSearchAndMenuCapture(t *testing.T) {
	calls := 0
	s := &Service{Key: "test-only", Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(r.URL.Host, "brave.com") {
			if r.Header.Get("X-Subscription-Token") != "test-only" {
				t.Fatal("missing key")
			}
			return response(r, `{"web":{"results":[{"url":"https://example.com/"},{"url":"https://example.com/"},{"url":"http://127.0.0.1/"}]}}`, "application/json"), nil
		}
		if r.Header.Get("X-Subscription-Token") != "" {
			t.Fatal("leaked search key")
		}
		if r.URL.Path == "/menu" {
			return response(r, "<p>Rice bread ¥500</p>", "text/html"), nil
		}
		return response(r, restaurantHTML, "text/html"), nil
	})}}
	urls, err := s.Search(context.Background(), "Shibuya gluten free", 5)
	if err != nil || len(urls) != 1 {
		t.Fatalf("%v %v", urls, err)
	}
	c, err := s.Capture(context.Background(), urls[0])
	if err != nil || !strings.Contains(c.MenuText, "Rice bread") || calls != 3 {
		t.Fatalf("%+v %v calls=%d", c, err, calls)
	}
}
func TestOversizeAndRedirect(t *testing.T) {
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, strings.Repeat("x", (2<<20)+1), "text/html"), nil
	})}
	if _, _, _, err := fetch(context.Background(), client, "https://example.com"); err == nil {
		t.Fatal("accepted oversized page")
	}
	req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
	if err := NewClient().CheckRedirect(req, []*http.Request{{}}); err == nil {
		t.Fatal("allowed private redirect")
	}
}

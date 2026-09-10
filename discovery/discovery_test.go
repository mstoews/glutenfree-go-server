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

func TestSearchQuery(t *testing.T) {
	q := SearchQuery("Chiyoda", "千代田区", "")
	for _, want := range []string{"Chiyoda", "千代田区", "Tokyo", "gluten free", "グルテンフリー", "restaurant"} {
		if !strings.Contains(q, want) {
			t.Errorf("query missing %q: %s", want, q)
		}
	}
	// An empty keyword must not leave a double space in the query.
	if strings.Contains(q, "  ") {
		t.Errorf("collapsed whitespace expected: %q", q)
	}
	// Directory and marketplace pages list many businesses, so Extract rejects
	// them; several also answer datacenter IPs with 403.
	for _, host := range []string{"ubereats.com", "yelp.com", "tripadvisor.jp", "findmeglutenfree.com", "tabelog.com"} {
		if !strings.Contains(q, "-site:"+host) {
			t.Errorf("aggregator %s not excluded: %s", host, q)
		}
	}
	// atly.com serves individual restaurant pages that capture cleanly; it must
	// not be swept up with the directories.
	if strings.Contains(q, "-site:atly.com") {
		t.Error("atly.com excluded, but it yields capturable restaurant pages")
	}
	if got := SearchQuery("Chiyoda", "千代田区", "ramen"); !strings.Contains(got, "ramen") {
		t.Errorf("operator keywords dropped: %s", got)
	}
}

const listingLDHTML = `<html><script type="application/ld+json">
{"@context":"https://schema.org","@type":"ItemList","itemListElement":[
 {"@type":"ListItem","position":1,"url":"https://example.com/restaurant/one"},
 {"@type":"ListItem","position":2,"item":{"@type":"Restaurant","name":"Two","url":"https://example.com/restaurant/two"}},
 {"@type":"ListItem","position":3,"url":"https://www.ubereats.com/jp/store/x"},
 {"@type":"ListItem","position":4,"url":"https://www.instagram.com/foo"},
 {"@type":"ListItem","position":5,"url":"https://example.com/hero.jpg"}
]}</script><body><a href="/anchor-only-link">Ignored</a></body></html>`

// A directory linking its own listing page to its own per-restaurant pages,
// which is where the working captures actually came from.
const sameHostLDHTML = `<html><script type="application/ld+json">
{"@context":"https://schema.org","@type":"ItemList","itemListElement":[
 {"@type":"ListItem","position":1,"url":"/gluten-free/location/alpha"}
]}</script></html>`

const anchorOnlyHTML = `<html><body>
<a href="/gluten-free/location/alpha">Alpha</a><a href="https://alpha-restaurant.jp/">Alpha site</a>
</body></html>`

func TestCandidateLinks(t *testing.T) {
	got := CandidateLinks([]byte(listingLDHTML), "https://example.com/best/list", 6)
	want := []string{"https://example.com/restaurant/one", "https://example.com/restaurant/two"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v (aggregator, social and asset links must be dropped)", got, want)
	}

	// Same-host links matter: directory listing pages link to their own location
	// pages, which are exactly the individual restaurant pages Extract accepts.
	got = CandidateLinks([]byte(sameHostLDHTML), "https://www.atly.com/best/gluten-free/list", 6)
	if len(got) != 1 || got[0] != "https://www.atly.com/gluten-free/location/alpha" {
		t.Errorf("same-host listing entry not followed: %v", got)
	}

	// Anchors are deliberately not scanned: on live listing pages they returned
	// only site navigation and exhausted the caller's fetch budget.
	if links := CandidateLinks([]byte(anchorOnlyHTML), "https://blog.example/guide", 6); len(links) != 0 {
		t.Errorf("anchors must not be harvested: %v", links)
	}

	if links := CandidateLinks([]byte(listingLDHTML), "https://example.com/best/list", 1); len(links) != 1 {
		t.Errorf("max ignored: %v", links)
	}
	if links := CandidateLinks([]byte(listingLDHTML), "https://example.com/best/list", 0); len(links) != 0 {
		t.Errorf("zero budget must harvest nothing: %v", links)
	}
	// A single-restaurant page is not a listing; nothing useful to follow.
	if links := CandidateLinks([]byte(restaurantHTML), "https://example.com/restaurant", 6); len(links) != 0 {
		t.Errorf("captured page should yield no listing links: %v", links)
	}
}

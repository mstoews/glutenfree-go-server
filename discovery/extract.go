package discovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}
func clip(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max])
	}
	return s
}
func nodeText(n *html.Node) string {
	if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript") {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data + " "
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c))
	}
	return b.String()
}
func pageText(data []byte, limit int) string {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	return clip(strings.Join(strings.Fields(nodeText(doc)), " "), limit)
}
func stringValue(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
func links(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, item := range x {
			out = append(out, links(item)...)
		}
		return out
	case map[string]any:
		for _, k := range []string{"url", "contentUrl", "@id"} {
			if s := stringValue(x[k]); s != "" {
				return []string{s}
			}
		}
	}
	return nil
}
func addURL(out *[]string, base *url.URL, raw string) {
	if len(*out) >= 10 || strings.TrimSpace(raw) == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	u = base.ResolveReference(u)
	if _, err = URL(u.String()); err != nil {
		return
	}
	for _, v := range *out {
		if v == u.String() {
			return
		}
	}
	*out = append(*out, u.String())
}
func restaurant(v any, found *[]map[string]any) {
	switch x := v.(type) {
	case []any:
		for _, child := range x {
			restaurant(child, found)
		}
	case map[string]any:
		for _, typ := range links(x["@type"]) {
			switch strings.TrimPrefix(typ, "https://schema.org/") {
			case "Restaurant", "CafeOrCoffeeShop", "Bakery", "FoodEstablishment":
				*found = append(*found, x)
			}
		}
		// Only traverse graph containers, not reviews or unrelated nested businesses.
		if graph, ok := x["@graph"]; ok {
			restaurant(graph, found)
		}
	}
}

// Extract requires one identifiable business; listicles and ambiguous multi-branch
// pages are rejected rather than turned into invented restaurant records.
func Extract(data []byte, source string) (Candidate, error) {
	base, err := URL(source)
	if err != nil {
		return Candidate{}, err
	}
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return Candidate{}, err
	}
	var found []map[string]any
	walk(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" && strings.EqualFold(attr(n, "type"), "application/ld+json") && n.FirstChild != nil {
			var v any
			if json.Unmarshal([]byte(n.FirstChild.Data), &v) == nil {
				restaurant(v, &found)
			}
		}
	})
	if len(found) != 1 {
		return Candidate{}, errors.New("page must identify exactly one restaurant using structured data; capture it manually if unavailable")
	}
	r := found[0]
	c := Candidate{Name: clip(stringValue(r["name"]), 300), SourceURL: base.String(), Phone: clip(stringValue(r["telephone"]), 100), Cuisine: clip(strings.Join(links(r["servesCuisine"]), ", "), 200), Images: []string{}, Menus: []string{}, Warnings: []string{}}
	switch a := r["address"].(type) {
	case string:
		c.Address = a
	case map[string]any:
		var parts []string
		for _, k := range []string{"postalCode", "addressRegion", "addressLocality", "streetAddress"} {
			if s := stringValue(a[k]); s != "" {
				parts = append(parts, s)
			}
		}
		c.Address = strings.Join(parts, " ")
	}
	c.Address = clip(c.Address, 1000)
	if c.Name == "" || c.Address == "" {
		return c, errors.New("restaurant name or address is missing")
	}
	text := pageText(data, 60000)
	lower := strings.ToLower(text)
	for _, needle := range []string{"gluten-free", "gluten free", "グルテンフリー"} {
		if i := strings.Index(lower, needle); i >= 0 {
			start := i - 150
			if start < 0 {
				start = 0
			}
			c.Evidence = clip(strings.ToValidUTF8(text[start:], ""), 600)
			break
		}
	}
	if c.Evidence == "" {
		return c, errors.New("no gluten-free statement found on the source page")
	}
	for _, raw := range links(r["image"]) {
		addURL(&c.Images, base, raw)
	}
	for _, key := range []string{"hasMenu", "menu"} {
		for _, raw := range links(r[key]) {
			addURL(&c.Menus, base, raw)
		}
	}
	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.Data == "meta" && attr(n, "property") == "og:image" {
			addURL(&c.Images, base, attr(n, "content"))
		}
		if n.Data == "a" {
			label := strings.ToLower(nodeText(n) + " " + attr(n, "href"))
			if strings.Contains(label, "menu") || strings.Contains(label, "メニュー") {
				addURL(&c.Menus, base, attr(n, "href"))
			}
		}
	})
	c.Warnings = append(c.Warnings, "Gluten-free evidence is unverified. Check preparation and cross-contact with the restaurant.", "Image URLs are research sources; verify reuse permission before publishing.")
	return c, nil
}

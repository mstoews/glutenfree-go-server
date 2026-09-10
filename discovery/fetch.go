// Package discovery finds restaurant source material for operator review.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// URL restricts crawler destinations; DNS addresses are checked again at dial time.
func URL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || (u.Port() != "" && u.Port() != "80" && u.Port() != "443") {
		return nil, errors.New("expected a public HTTP(S) URL on port 80 or 443")
	}
	u.Fragment = ""
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicIP(ip) {
		return nil, errors.New("private network URLs are not allowed")
	}
	return u, nil
}
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2002::/16", "64:ff9b::/96"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}
func NewClient() *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("host has no addresses")
			}
			for _, ip := range ips {
				if !publicIP(ip) {
					return nil, errors.New("private network destinations are blocked")
				}
			}
			// Dial the validated address, not the hostname, to prevent DNS rebinding.
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		_, err := URL(req.URL.String())
		return err
	}}
}

func fetch(ctx context.Context, client *http.Client, raw string) ([]byte, string, string, error) {
	u, err := URL(raw)
	if err != nil {
		return nil, "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("User-Agent", "GurufuriDiscovery/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf")
	res, err := client.Do(req)
	if err != nil {
		return nil, "", "", errors.New("source could not be fetched")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, "", "", fmt.Errorf("source returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) > 2<<20 {
		return nil, "", "", errors.New("source exceeds 2 MiB limit")
	}
	return data, res.Request.URL.String(), res.Header.Get("Content-Type"), nil
}

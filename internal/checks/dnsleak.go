package checks

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// DNS-leak detection (heuristic): through the proxy, compare the country of the
// DNS resolver that served the lookup (ip-api's EDNS endpoint) against the
// country of the exit IP (ip-api). A resolver in a different country than the
// exit means DNS is escaping the tunnel. Both come from ip-api so the country
// names are directly comparable. It is a heuristic, not a guarantee.

// DefaultDNSEDNSURL reports the resolver as seen by the authoritative server.
const DefaultDNSEDNSURL = "https://edns.ip-api.com/json/"

// defaultDNSExitURL reports the caller's (exit) country.
const defaultDNSExitURL = "http://ip-api.com/json/?fields=status,country"

type ednsResponse struct {
	DNS struct {
		Geo string `json:"geo"` // e.g. "United States - Google"
		IP  string `json:"ip"`
	} `json:"dns"`
}

type exitCountryResponse struct {
	Status  string `json:"status"`
	Country string `json:"country"`
}

// DNSLeakResult is the outcome of a DNS-leak probe. OK is false when the lookup
// could not be completed (so callers can skip recording it).
type DNSLeakResult struct {
	OK     bool
	Leak   bool
	Detail string
}

// DNSLeakCheck probes for a DNS leak through the proxy. URLs default when empty.
type DNSLeakCheck struct {
	EDNSURL string
	ExitURL string
	Timeout time.Duration
}

func (c DNSLeakCheck) Run(ctx context.Context, client *http.Client) (DNSLeakResult, error) {
	ednsURL, exitURL := c.EDNSURL, c.ExitURL
	if ednsURL == "" {
		ednsURL = DefaultDNSEDNSURL
	}
	if exitURL == "" {
		exitURL = defaultDNSExitURL
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	exitCountry := ""
	if status, body := fetch(ctx, client, exitURL); status != 0 {
		var er exitCountryResponse
		if json.Unmarshal([]byte(body), &er) == nil && er.Status == "success" {
			exitCountry = er.Country
		}
	}
	dnsGeo := ""
	if status, body := fetch(ctx, client, ednsURL); status != 0 {
		var er ednsResponse
		if json.Unmarshal([]byte(body), &er) == nil {
			dnsGeo = er.DNS.Geo
		}
	}
	return classifyDNSLeak(dnsGeo, exitCountry), nil
}

// classifyDNSLeak compares the DNS resolver country (the part of dns.geo before
// " - ") with the exit country. It is pure and unit-tested.
func classifyDNSLeak(dnsGeo, exitCountry string) DNSLeakResult {
	dnsCountry := strings.TrimSpace(strings.SplitN(dnsGeo, " - ", 2)[0])
	if dnsCountry == "" || exitCountry == "" {
		return DNSLeakResult{OK: false, Detail: "unreachable"}
	}
	if !strings.EqualFold(dnsCountry, exitCountry) {
		return DNSLeakResult{OK: true, Leak: true, Detail: "leak: dns " + dnsCountry + " vs exit " + exitCountry}
	}
	return DNSLeakResult{OK: true, Leak: false, Detail: "no leak (" + dnsCountry + ")"}
}

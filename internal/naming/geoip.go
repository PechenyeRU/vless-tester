package naming

import (
	"context"
	"fmt"
	"net"

	"github.com/oschwald/geoip2-golang"
)

// CountryResolver maps an IP address to an ISO-3166 alpha-2 country code. The
// interface keeps the naming logic testable without a real MaxMind database.
type CountryResolver interface {
	LookupCountry(ip net.IP) (string, error)
}

// MaxMindResolver resolves countries from a MaxMind GeoLite2-Country database.
type MaxMindResolver struct {
	reader *geoip2.Reader
}

// OpenMaxMind opens a GeoLite2-Country .mmdb file.
func OpenMaxMind(path string) (*MaxMindResolver, error) {
	r, err := geoip2.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: open %s: %w", path, err)
	}
	return &MaxMindResolver{reader: r}, nil
}

// LookupCountry returns the ISO country code for an IP.
func (m *MaxMindResolver) LookupCountry(ip net.IP) (string, error) {
	rec, err := m.reader.Country(ip)
	if err != nil {
		return "", fmt.Errorf("geoip: lookup: %w", err)
	}
	return rec.Country.IsoCode, nil
}

// Close releases the database handle.
func (m *MaxMindResolver) Close() error { return m.reader.Close() }

// ResolveCountry returns the country for a server host, which may be an IP
// literal or a hostname. Hostnames are resolved to their first IP. An empty
// country (with nil error) means "unknown", so callers can fall back gracefully.
func ResolveCountry(ctx context.Context, resolver CountryResolver, host string) (string, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addrs) == 0 {
			return "", fmt.Errorf("geoip: resolve host %q: %w", host, err)
		}
		ip = addrs[0].IP
	}
	return resolver.LookupCountry(ip)
}

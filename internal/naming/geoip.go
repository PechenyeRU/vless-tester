package naming

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

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

// ReloadingResolver resolves countries from a MaxMind database that may not
// exist yet at startup and may be replaced later. It opens the file lazily and
// re-opens it whenever the modtime changes, so a database downloaded after the
// process started (the geoip-refresh job) or refreshed on schedule is picked up
// without a restart. A missing file yields an error (callers treat that as
// "unknown country"). Safe for concurrent use.
type ReloadingResolver struct {
	path string
	mu   sync.Mutex
	cur  *MaxMindResolver
	mod  time.Time
}

// NewReloadingResolver returns a resolver backed by the database at path,
// opening it on first use and reloading it when the file changes.
func NewReloadingResolver(path string) *ReloadingResolver {
	return &ReloadingResolver{path: path}
}

// LookupCountry resolves an IP, (re)opening the database when it first appears
// or its modtime changes.
func (r *ReloadingResolver) LookupCountry(ip net.IP) (string, error) {
	info, err := os.Stat(r.path)
	if err != nil {
		return "", fmt.Errorf("geoip: database unavailable: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil || !info.ModTime().Equal(r.mod) {
		mm, err := OpenMaxMind(r.path)
		if err != nil {
			return "", err
		}
		if r.cur != nil {
			_ = r.cur.Close()
		}
		r.cur, r.mod = mm, info.ModTime()
	}
	return r.cur.LookupCountry(ip)
}

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

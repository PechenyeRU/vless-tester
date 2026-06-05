package naming

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultMaxMindEdition is the country-level GeoLite2 database edition.
const DefaultMaxMindEdition = "GeoLite2-Country"

// MaxMindDownloader fetches and caches a GeoLite2 database using MaxMind account
// credentials (basic auth). The coordinator refreshes the cache on a schedule.
type MaxMindDownloader struct {
	AccountID  string
	LicenseKey string
	Edition    string // defaults to DefaultMaxMindEdition
	HTTPClient *http.Client
}

// NeedsRefresh reports whether the database at path is missing or older than
// maxAge. It is pure and side-effect free, so the scheduler can poll it cheaply.
func NeedsRefresh(path string, maxAge time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return time.Since(info.ModTime()) > maxAge
}

// EnsureDatabase downloads the database to destPath when it is missing or stale.
func (d *MaxMindDownloader) EnsureDatabase(ctx context.Context, destPath string, maxAge time.Duration) error {
	if !NeedsRefresh(destPath, maxAge) {
		return nil
	}
	return d.Download(ctx, destPath)
}

// Download fetches the latest database tarball and writes the extracted .mmdb to
// destPath atomically.
func (d *MaxMindDownloader) Download(ctx context.Context, destPath string) error {
	if d.AccountID == "" || d.LicenseKey == "" {
		return fmt.Errorf("geoip: missing MaxMind credentials")
	}
	edition := d.Edition
	if edition == "" {
		edition = DefaultMaxMindEdition
	}
	client := d.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	url := fmt.Sprintf("https://download.maxmind.com/geoip/databases/%s/download?suffix=tar.gz", edition)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("geoip: build request: %w", err)
	}
	req.SetBasicAuth(d.AccountID, d.LicenseKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("geoip: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("geoip: download status %d", resp.StatusCode)
	}

	return extractMMDB(resp.Body, destPath)
}

// extractMMDB pulls the first .mmdb entry from a gzipped tar stream and writes it
// to destPath via a temp file + rename so readers never see a partial database.
func extractMMDB(r io.Reader, destPath string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("geoip: gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("geoip: no .mmdb entry in archive")
		}
		if err != nil {
			return fmt.Errorf("geoip: tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".mmdb") {
			continue
		}
		return writeAtomic(destPath, tr)
	}
}

func writeAtomic(destPath string, r io.Reader) error {
	if dir := filepath.Dir(destPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("geoip: mkdir: %w", err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".mmdb-*")
	if err != nil {
		return fmt.Errorf("geoip: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("geoip: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("geoip: close temp: %w", err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("geoip: rename: %w", err)
	}
	return nil
}

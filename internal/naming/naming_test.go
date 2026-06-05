package naming_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/whitedns/vless-tester/internal/naming"
)

func TestEmoji(t *testing.T) {
	tests := []struct {
		country string
		want    string
	}{
		{"FR", "🇫🇷"},
		{"US", "🇺🇸"},
		{"ch", "🇨🇭"}, // lowercase accepted
		{"", ""},
		{"X", ""},
		{"USA", ""}, // not alpha-2
		{"1F", ""},  // non-letter
	}
	for _, tt := range tests {
		if got := naming.Emoji(tt.country); got != tt.want {
			t.Errorf("Emoji(%q) = %q, want %q", tt.country, got, tt.want)
		}
	}
}

func TestSeqAllocatorStableAndIncrementing(t *testing.T) {
	alloc := naming.Allocator{Backend: naming.NewMemoryBackend()}
	ctx := context.Background()

	first, err := alloc.Assign(ctx, "fp-a", "FR")
	if err != nil {
		t.Fatalf("assign a: %v", err)
	}
	if first != "FR1" {
		t.Fatalf("first FR = %q, want FR1", first)
	}

	// Same fingerprint must keep its name (idempotent / stable).
	again, _ := alloc.Assign(ctx, "fp-a", "FR")
	if again != "FR1" {
		t.Fatalf("stable assign = %q, want FR1", again)
	}

	// A new fingerprint in the same country increments.
	second, _ := alloc.Assign(ctx, "fp-b", "FR")
	if second != "FR2" {
		t.Fatalf("second FR = %q, want FR2", second)
	}

	// A different country has its own counter.
	ch, _ := alloc.Assign(ctx, "fp-c", "CH")
	if ch != "CH1" {
		t.Fatalf("first CH = %q, want CH1", ch)
	}
}

// fakeResolver maps fixed IPs to country codes for deterministic tests.
type fakeResolver map[string]string

func (f fakeResolver) LookupCountry(ip net.IP) (string, error) {
	return f[ip.String()], nil
}

func TestResolveCountryIPLiteral(t *testing.T) {
	resolver := fakeResolver{"8.8.8.8": "US"}
	country, err := naming.ResolveCountry(context.Background(), resolver, "8.8.8.8")
	if err != nil {
		t.Fatalf("ResolveCountry: %v", err)
	}
	if country != "US" {
		t.Fatalf("country = %q, want US", country)
	}
}

func TestNeedsRefresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.mmdb")

	// Missing file always needs refresh.
	if !naming.NeedsRefresh(path, time.Hour) {
		t.Fatal("missing file should need refresh")
	}

	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Fresh file within maxAge does not.
	if naming.NeedsRefresh(path, time.Hour) {
		t.Fatal("fresh file should not need refresh")
	}
	// Backdate the file beyond maxAge.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if !naming.NeedsRefresh(path, time.Hour) {
		t.Fatal("stale file should need refresh")
	}
}

// TestMaxMindDownloadGated hits the real MaxMind API only when credentials are
// configured; otherwise it skips.
func TestMaxMindDownloadGated(t *testing.T) {
	acc := os.Getenv("MAXMIND_ACCOUNT_ID")
	key := os.Getenv("MAXMIND_LICENSE_KEY")
	if acc == "" || key == "" {
		t.Skip("MAXMIND credentials not set; skipping live download")
	}
	d := &naming.MaxMindDownloader{AccountID: acc, LicenseKey: key}
	dest := filepath.Join(t.TempDir(), "GeoLite2-Country.mmdb")
	if err := d.Download(context.Background(), dest); err != nil {
		t.Fatalf("download: %v", err)
	}
	r, err := naming.OpenMaxMind(dest)
	if err != nil {
		t.Fatalf("open downloaded db: %v", err)
	}
	defer r.Close()
	if _, err := r.LookupCountry(net.ParseIP("8.8.8.8")); err != nil {
		t.Fatalf("lookup on real db: %v", err)
	}
}

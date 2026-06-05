package naming

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// makeArchive builds an in-memory tar.gz containing the given entries.
func makeArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractMMDB(t *testing.T) {
	want := []byte("fake-mmdb-payload")
	archive := makeArchive(t, map[string][]byte{
		"GeoLite2-Country_20260101/README.txt":            []byte("ignore me"),
		"GeoLite2-Country_20260101/GeoLite2-Country.mmdb": want,
	})

	dest := filepath.Join(t.TempDir(), "out.mmdb")
	if err := extractMMDB(bytes.NewReader(archive), dest); err != nil {
		t.Fatalf("extractMMDB: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("extracted = %q, want %q", got, want)
	}
}

func TestExtractMMDBMissingEntry(t *testing.T) {
	archive := makeArchive(t, map[string][]byte{"only/readme.txt": []byte("x")})
	dest := filepath.Join(t.TempDir(), "out.mmdb")
	if err := extractMMDB(bytes.NewReader(archive), dest); err == nil {
		t.Fatal("expected error when archive has no .mmdb entry")
	}
}

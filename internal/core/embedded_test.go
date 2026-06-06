package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// extractEmbedded is exercised with synthetic bytes so the suite stays green
// without a real embedded binary (default builds carry none).

func TestExtractEmbeddedWritesExecutable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	data := []byte("fake sing-box payload")

	path, err := extractEmbedded(data, sha256Hex(data))
	if err != nil {
		t.Fatalf("extractEmbedded: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("content mismatch: %q", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("extracted file not executable: %v", fi.Mode())
		}
	}
}

func TestExtractEmbeddedRejectsChecksumMismatch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := extractEmbedded([]byte("payload"), "deadbeef"); err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
}

func TestExtractEmbeddedIsIdempotent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	data := []byte("idempotent payload")
	sum := sha256Hex(data)

	first, err := extractEmbedded(data, sum)
	if err != nil {
		t.Fatalf("first extract: %v", err)
	}
	second, err := extractEmbedded(data, sum)
	if err != nil {
		t.Fatalf("second extract: %v", err)
	}
	if first != second {
		t.Fatalf("path changed across calls: %q vs %q", first, second)
	}
}

func TestExtractEmbeddedRebuildsCorruptedCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	data := []byte("rebuild payload")
	sum := sha256Hex(data)

	path, err := extractEmbedded(data, sum)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	// Corrupt the cached copy; the next extraction must detect the bad hash and
	// rewrite it rather than hand back a tampered executable.
	if err := os.WriteFile(path, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	again, err := extractEmbedded(data, sum)
	if err != nil {
		t.Fatalf("re-extract: %v", err)
	}
	got, err := os.ReadFile(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("corrupted cache not rebuilt: %q", got)
	}
}

func TestExtractEmbeddedEmptyIsNotFound(t *testing.T) {
	if _, err := extractEmbedded(nil, ""); err != ErrBinaryNotFound {
		t.Fatalf("want ErrBinaryNotFound, got %v", err)
	}
}

func TestEmbeddedBlobPresent(t *testing.T) {
	if (embeddedBlob{}).present() {
		t.Fatal("empty blob should not be present")
	}
	if !(embeddedBlob{data: []byte{1}}).present() {
		t.Fatal("non-empty blob should be present")
	}
}

// cacheRoot honors XDG_CACHE_HOME on linux, which the tests rely on for isolation.
func TestCacheRootUsesUserCacheDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG_CACHE_HOME semantics are linux-specific")
	}
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	if root := cacheRoot(); root != tmp {
		t.Fatalf("cacheRoot = %q, want %q", root, filepath.Clean(tmp))
	}
}

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// embeddedBlob holds the optional embedded sing-box executable. It is populated
// at build time only when the binary is compiled with the `embed_singbox` tag
// (see embed_on.go / embed_off.go); otherwise it is empty and the worker falls
// back to SINGBOX_BIN or the PATH.
type embeddedBlob struct {
	data []byte
	sum  string // expected lowercase hex sha256, may be empty
}

// embedded is the zero value (absent) in the default build; the single-file
// build populates it from embed_on.go's init when compiled with `embed_singbox`.
var embedded embeddedBlob

func (b embeddedBlob) present() bool { return len(b.data) > 0 }

// extractEmbedded materializes the embedded sing-box on disk and returns its
// path. The destination is keyed by the binary's sha256 so upgrades never
// collide and repeated starts reuse the same file. The content is verified
// against the embedded checksum before use and the on-disk copy is re-hashed on
// reuse, so a truncated or tampered cache is rebuilt rather than executed.
func extractEmbedded(data []byte, sum string) (string, error) {
	if len(data) == 0 {
		return "", ErrBinaryNotFound
	}
	gotHex := sha256Hex(data)
	if sum != "" && !strings.EqualFold(gotHex, sum) {
		return "", fmt.Errorf("core: embedded sing-box checksum mismatch: got %s want %s", gotHex, sum)
	}

	dir := filepath.Join(cacheRoot(), "vless-tester")
	name := "sing-box-" + gotHex[:16]
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := filepath.Join(dir, name)

	if fileMatches(dest, gotHex) {
		return dest, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("core: extract dir: %w", err)
	}
	// Write to a temp file in the same dir then rename, so a concurrent or
	// crashed extraction never leaves a partial executable at dest.
	tmp, err := os.CreateTemp(dir, name+".*")
	if err != nil {
		return "", fmt.Errorf("core: extract temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("core: extract write: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("core: extract chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("core: extract close: %w", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("core: extract rename: %w", err)
	}
	return dest, nil
}

// fileMatches reports whether path exists and its content hashes to wantHex.
func fileMatches(path, wantHex string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), wantHex)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// cacheRoot returns the base directory for extracted artifacts, preferring the
// user cache dir and falling back to the OS temp dir.
func cacheRoot() string {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return dir
	}
	return os.TempDir()
}

//go:build embed_singbox

package core

import (
	_ "embed"
	"strings"
)

// singboxBinary is the sing-box executable, embedded for the target os/arch.
// The Makefile `dist`/`docker-worker` targets fetch the matching static release
// into embedded/ before building with -tags embed_singbox.
//
//go:embed embedded/sing-box
var singboxBinary []byte

// singboxChecksum is the lowercase hex sha256 of singboxBinary, written next to
// it by the fetch script and verified at extraction time.
//
//go:embed embedded/sing-box.sha256
var singboxChecksum string

func init() {
	embedded = embeddedBlob{
		data: singboxBinary,
		sum:  strings.TrimSpace(singboxChecksum),
	}
}

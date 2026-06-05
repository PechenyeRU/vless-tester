package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/whitedns/vless-tester/internal/model"
)

// fingerprint derives a deterministic identity for a server that is independent
// of the cosmetic name fragment, so duplicate links to the same endpoint (with
// different remarks) collapse to one row. It folds protocol, host, port, the
// credential and the sorted connection params into a SHA-256 hex digest.
func fingerprint(s model.Server, credential string) string {
	keys := make([]string, 0, len(s.Params))
	for k := range s.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(string(s.Protocol))
	b.WriteByte('|')
	b.WriteString(strings.ToLower(s.Host))
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(s.Port))
	b.WriteByte('|')
	b.WriteString(credential)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(s.Params[k])
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

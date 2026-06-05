package ingest

import "encoding/base64"

// decodeBase64 tolerantly decodes base64 text that may be standard or URL-safe
// and may omit padding, covering the many encodings seen in share links and
// subscription bodies.
func decodeBase64(s string) ([]byte, bool) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

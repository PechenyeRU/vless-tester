package ingest

import (
	"bufio"
	"strings"

	"github.com/whitedns/vless-tester/internal/model"
)

// ParseList parses many newline-separated share links, returning the valid
// servers and the 1-based line numbers that failed to parse. Blank lines and
// lines starting with '#' (comments) are ignored.
func ParseList(text string) (servers []model.Server, failedLines []int) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	// Share links can be long; raise the line cap well above the default 64KB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		srv, err := Parse(raw)
		if err != nil {
			failedLines = append(failedLines, line)
			continue
		}
		servers = append(servers, srv)
	}
	return servers, failedLines
}

// ParseSubscription decodes a base64 subscription body and parses its links. If
// the body is not base64 it is treated as plain newline-separated text, which
// some providers serve directly.
func ParseSubscription(body string) (servers []model.Server, failedLines []int) {
	body = strings.TrimSpace(body)
	if decoded, ok := decodeBase64(body); ok && looksLikeLinks(string(decoded)) {
		return ParseList(string(decoded))
	}
	return ParseList(body)
}

// looksLikeLinks reports whether text contains at least one supported scheme,
// guarding against treating arbitrary base64 garbage as a link list.
func looksLikeLinks(text string) bool {
	for _, scheme := range []string{"vless://", "vmess://", "trojan://", "tuic://", "hysteria2://", "hy2://", "ss://"} {
		if strings.Contains(text, scheme) {
			return true
		}
	}
	return false
}

// Dedup removes servers sharing a fingerprint, keeping the first occurrence and
// preserving input order. It returns the unique servers and how many were dropped.
func Dedup(servers []model.Server) (unique []model.Server, dropped int) {
	seen := make(map[string]struct{}, len(servers))
	for _, s := range servers {
		if _, ok := seen[s.Fingerprint]; ok {
			dropped++
			continue
		}
		seen[s.Fingerprint] = struct{}{}
		unique = append(unique, s)
	}
	return unique, dropped
}

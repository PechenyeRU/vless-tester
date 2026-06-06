package ingest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/whitedns/vless-tester/internal/model"
)

// ErrUnsupported is returned for links whose scheme is not a known protocol.
var ErrUnsupported = fmt.Errorf("unsupported proxy scheme")

// ErrMalformed is returned for links carrying bytes that cannot be persisted:
// a NUL or any invalid UTF-8 sequence. Subscriptions occasionally serve such
// garbage, and Postgres text columns reject it (SQLSTATE 22021), so we drop it
// at parse time rather than letting it abort a later upsert.
var ErrMalformed = fmt.Errorf("malformed link")

// Parse turns a single share link into a normalized, fingerprinted Server.
func Parse(raw string) (model.Server, error) {
	raw = strings.TrimSpace(raw)
	// A NUL is a valid UTF-8 codepoint but Postgres rejects it, so check it
	// explicitly alongside the general UTF-8 validity guard.
	if strings.IndexByte(raw, 0) >= 0 || !utf8.ValidString(raw) {
		return model.Server{}, fmt.Errorf("%q: %w", clip(raw), ErrMalformed)
	}
	scheme, _, ok := strings.Cut(raw, "://")
	if !ok {
		return model.Server{}, fmt.Errorf("missing scheme in %q: %w", clip(raw), ErrUnsupported)
	}
	switch strings.ToLower(scheme) {
	case "vless":
		return parseURLStyle(raw, model.ProtocolVLESS)
	case "trojan":
		return parseURLStyle(raw, model.ProtocolTrojan)
	case "tuic":
		return parseURLStyle(raw, model.ProtocolTUIC)
	case "hysteria2", "hy2":
		return parseURLStyle(raw, model.ProtocolHysteria2)
	case "anytls":
		return parseURLStyle(raw, model.ProtocolAnyTLS)
	case "hysteria", "hy":
		return parseHysteriaV1(raw)
	case "socks", "socks5":
		return parseSocks(raw)
	case "vmess":
		return parseVMess(raw)
	case "ss":
		return parseShadowsocks(raw)
	default:
		return model.Server{}, fmt.Errorf("scheme %q: %w", scheme, ErrUnsupported)
	}
}

// parseURLStyle handles the userinfo@host:port?query#name family (vless, trojan,
// tuic, hysteria2). The full userinfo is used as the credential so that
// uuid:password forms (tuic) and auth strings (hysteria2) round-trip stably.
func parseURLStyle(raw string, proto model.Protocol) (model.Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return model.Server{}, fmt.Errorf("parse %s: %w", proto, err)
	}
	host := u.Hostname()
	if host == "" {
		return model.Server{}, fmt.Errorf("%s: empty host", proto)
	}
	port, err := parsePort(u.Port())
	if err != nil {
		return model.Server{}, fmt.Errorf("%s: %w", proto, err)
	}

	credential := ""
	if u.User != nil {
		credential = u.User.String()
	}

	srv := model.Server{
		RawURI:     raw,
		Protocol:   proto,
		Host:       host,
		Port:       port,
		Params:     flattenQuery(u.Query()),
		Credential: credential,
	}
	srv.Fingerprint = fingerprint(srv, credential)
	return srv, nil
}

// vmessJSON is the base64-wrapped JSON payload of a vmess:// link.
type vmessJSON struct {
	Add  string `json:"add"`
	Port any    `json:"port"` // may be string or number across exporters
	ID   string `json:"id"`
	Aid  any    `json:"aid"`
	Net  string `json:"net"`
	Type string `json:"type"`
	Host string `json:"host"`
	Path string `json:"path"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	Scy  string `json:"scy"`
}

func parseVMess(raw string) (model.Server, error) {
	payload := strings.TrimPrefix(raw, "vmess://")
	decoded, ok := decodeBase64(payload)
	if !ok {
		return model.Server{}, fmt.Errorf("vmess: invalid base64 payload")
	}
	var v vmessJSON
	if err := json.Unmarshal(decoded, &v); err != nil {
		return model.Server{}, fmt.Errorf("vmess: invalid json: %w", err)
	}
	if v.Add == "" {
		return model.Server{}, fmt.Errorf("vmess: empty host")
	}
	port, err := parsePort(toString(v.Port))
	if err != nil {
		return model.Server{}, fmt.Errorf("vmess: %w", err)
	}

	params := map[string]string{}
	putIf(params, "net", v.Net)
	putIf(params, "type", v.Type)
	putIf(params, "host", v.Host)
	putIf(params, "path", v.Path)
	putIf(params, "tls", v.TLS)
	putIf(params, "sni", v.SNI)
	putIf(params, "scy", v.Scy)
	putIf(params, "aid", toString(v.Aid))

	srv := model.Server{
		RawURI:     raw,
		Protocol:   model.ProtocolVMess,
		Host:       v.Add,
		Port:       port,
		Params:     params,
		Credential: v.ID,
	}
	srv.Fingerprint = fingerprint(srv, v.ID)
	return srv, nil
}

// parseShadowsocks handles both the SIP002 form (base64(method:password)@host:port)
// and the legacy fully-encoded form (base64(method:password@host:port)).
func parseShadowsocks(raw string) (model.Server, error) {
	body := strings.TrimPrefix(raw, "ss://")
	if i := strings.IndexByte(body, '#'); i >= 0 {
		body = body[:i] // drop the cosmetic name fragment
	}

	var userinfo, hostport, query string
	if at := strings.LastIndexByte(body, '@'); at >= 0 {
		// SIP002: userinfo is base64(method:password); host:port follows.
		userinfo = body[:at]
		hostport = body[at+1:]
	} else {
		// Legacy: the whole body is base64(method:password@host:port).
		decoded, ok := decodeBase64(body)
		if !ok {
			return model.Server{}, fmt.Errorf("ss: invalid base64 body")
		}
		at := strings.LastIndexByte(string(decoded), '@')
		if at < 0 {
			return model.Server{}, fmt.Errorf("ss: missing host in decoded body")
		}
		// Already decoded: this half is plain method:password. The ':' it
		// contains is not a valid base64 character, so decodeSSUserinfo will
		// correctly treat it as plain text rather than re-decoding it.
		userinfo = string(decoded)[:at]
		hostport = string(decoded)[at+1:]
	}

	if base, tail, ok := strings.Cut(hostport, "/"); ok {
		// Strip any /?plugin=... tail; keep the query for params.
		hostport = base
		if _, q, ok := strings.Cut(tail, "?"); ok {
			query = q
		}
	} else if base, q, ok := strings.Cut(hostport, "?"); ok {
		hostport = base
		query = q
	}

	method, password, err := decodeSSUserinfo(userinfo)
	if err != nil {
		return model.Server{}, err
	}
	host, portStr, ok := strings.Cut(hostport, ":")
	if !ok || host == "" {
		return model.Server{}, fmt.Errorf("ss: invalid host:port %q", hostport)
	}
	port, err := parsePort(portStr)
	if err != nil {
		return model.Server{}, fmt.Errorf("ss: %w", err)
	}

	params := map[string]string{"method": method}
	if query != "" {
		if vals, err := url.ParseQuery(query); err == nil {
			maps.Copy(params, flattenQuery(vals))
		}
	}

	srv := model.Server{
		RawURI:     raw,
		Protocol:   model.ProtocolShadowsocks,
		Host:       host,
		Port:       port,
		Params:     params,
		Credential: password,
	}
	srv.Fingerprint = fingerprint(srv, password)
	return srv, nil
}

// parseHysteriaV1 handles the legacy Hysteria v1 link, where the credential
// lives in the `auth`/`auth_str` query parameter rather than the userinfo.
func parseHysteriaV1(raw string) (model.Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return model.Server{}, fmt.Errorf("parse hysteria: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return model.Server{}, fmt.Errorf("hysteria: empty host")
	}
	port, err := parsePort(u.Port())
	if err != nil {
		return model.Server{}, fmt.Errorf("hysteria: %w", err)
	}
	params := flattenQuery(u.Query())
	credential := params["auth"]
	if credential == "" {
		credential = params["auth_str"]
	}

	srv := model.Server{
		RawURI:     raw,
		Protocol:   model.ProtocolHysteria,
		Host:       host,
		Port:       port,
		Params:     params,
		Credential: credential,
	}
	srv.Fingerprint = fingerprint(srv, credential)
	return srv, nil
}

// parseSocks handles socks://[base64(user:pass)@]host:port and the socks5://
// alias. Authentication is optional.
func parseSocks(raw string) (model.Server, error) {
	body := raw
	for _, prefix := range []string{"socks5://", "socks://"} {
		body = strings.TrimPrefix(body, prefix)
	}
	if i := strings.IndexByte(body, '#'); i >= 0 {
		body = body[:i]
	}
	if i := strings.IndexByte(body, '?'); i >= 0 {
		body = body[:i]
	}

	credential := ""
	if at := strings.LastIndexByte(body, '@'); at >= 0 {
		userinfo := body[:at]
		body = body[at+1:]
		if decoded, ok := decodeBase64(userinfo); ok && strings.Contains(string(decoded), ":") {
			userinfo = string(decoded)
		}
		credential = userinfo // "user:pass"
	}

	host, portStr, ok := strings.Cut(body, ":")
	if !ok || host == "" {
		return model.Server{}, fmt.Errorf("socks: invalid host:port %q", body)
	}
	port, err := parsePort(portStr)
	if err != nil {
		return model.Server{}, fmt.Errorf("socks: %w", err)
	}

	srv := model.Server{
		RawURI:     raw,
		Protocol:   model.ProtocolSOCKS,
		Host:       host,
		Port:       port,
		Params:     map[string]string{},
		Credential: credential,
	}
	srv.Fingerprint = fingerprint(srv, credential)
	return srv, nil
}

// decodeSSUserinfo extracts method and password from a shadowsocks userinfo
// segment, which is usually base64(method:password) but may be plain.
func decodeSSUserinfo(userinfo string) (method, password string, err error) {
	candidate := userinfo
	if decoded, ok := decodeBase64(userinfo); ok && strings.Contains(string(decoded), ":") {
		candidate = string(decoded)
	}
	m, p, ok := strings.Cut(candidate, ":")
	if !ok {
		return "", "", fmt.Errorf("ss: cannot split method:password")
	}
	return m, p, nil
}

func flattenQuery(values url.Values) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func parsePort(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty port")
	}
	port, err := strconv.Atoi(s)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return port, nil
}

func putIf(m map[string]string, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// toString renders a value that exporters may encode as string or number.
func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}

func clip(s string) string {
	if len(s) > 32 {
		return s[:32] + "..."
	}
	return s
}

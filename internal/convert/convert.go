// Package convert renders an approved server list into the subscription formats
// proxy clients consume: base64, sing-box, clash-meta, v2ray and surge. Every
// renderer is a pure function of public data only — the share URI, its parsed
// connection parameters and the public node name (e.g.
// "🇫🇷 | @WhiteDNS | FR110 | 12.3 MB/s"). They never read worker, vantage or
// diagnostic fields, so a served subscription cannot leak inner-working.
package convert

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/whitedns/vless-tester/internal/core"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/output"
)

// Node is one approved server to render: the normalized server plus its public
// display name.
type Node struct {
	Server model.Server
	Name   string
}

// Supported output targets.
const (
	TargetBase64  = "base64"
	TargetSingbox = "singbox"
	TargetClash   = "clash"
	TargetV2ray   = "v2ray"
	TargetSurge   = "surge"
)

// Targets lists every supported target in a stable order.
var Targets = []string{TargetBase64, TargetSingbox, TargetClash, TargetV2ray, TargetSurge}

// Supported reports whether target is a known output format.
func Supported(target string) bool {
	for _, t := range Targets {
		if t == target {
			return true
		}
	}
	return false
}

// ContentType returns the MIME type a target is served with.
func ContentType(target string) string {
	switch target {
	case TargetSingbox:
		return "application/json; charset=utf-8"
	case TargetClash:
		return "text/yaml; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}

// Render renders nodes into the target format. An unknown target is an error; an
// empty node list renders a valid, empty document.
func Render(target string, nodes []Node) ([]byte, error) {
	switch target {
	case TargetBase64:
		return renderBase64(nodes), nil
	case TargetV2ray:
		return renderV2ray(nodes), nil
	case TargetSingbox:
		return renderSingbox(nodes)
	case TargetClash:
		return renderClash(nodes)
	case TargetSurge:
		return renderSurge(nodes), nil
	default:
		return nil, fmt.Errorf("convert: unknown target %q", target)
	}
}

// renamedURIs reuses the base64 subscription's renaming so every format shows
// identical node names.
func renamedURIs(nodes []Node) []string {
	links := make([]string, 0, len(nodes))
	for _, n := range nodes {
		links = append(links, output.RenameLink(n.Server.RawURI, n.Name))
	}
	return links
}

// renderV2ray emits the plain newline-separated URI list.
func renderV2ray(nodes []Node) []byte {
	return []byte(strings.Join(renamedURIs(nodes), "\n"))
}

// renderBase64 emits the standard base64-wrapped URI list (the canonical
// subscription form most clients ingest).
func renderBase64(nodes []Node) []byte {
	joined := strings.Join(renamedURIs(nodes), "\n")
	return []byte(base64.StdEncoding.EncodeToString([]byte(joined)))
}

// renderSingbox emits a sing-box config whose outbounds reuse the validated core
// mappers, grouped under a urltest ("auto") and a manual selector.
func renderSingbox(nodes []Node) ([]byte, error) {
	outbounds := make([]any, 0, len(nodes)+3)
	tags := make([]string, 0, len(nodes))
	for _, n := range nodes {
		o, err := core.Outbound(n.Server, n.Name)
		if err != nil {
			continue // every supported protocol maps; skip the impossible case
		}
		outbounds = append(outbounds, o)
		tags = append(tags, n.Name)
	}
	if len(tags) > 0 {
		selectOutbounds := append([]string{"auto"}, tags...)
		outbounds = append(outbounds,
			map[string]any{
				"type":      "urltest",
				"tag":       "auto",
				"outbounds": tags,
				"url":       "https://www.gstatic.com/generate_204",
				"interval":  "10m",
			},
			map[string]any{
				"type":      "selector",
				"tag":       "select",
				"outbounds": selectOutbounds,
				"default":   "auto",
			},
		)
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	return json.MarshalIndent(map[string]any{"outbounds": outbounds}, "", "  ")
}

// orDefault returns v when non-empty, else def.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// firstNonEmpty returns the first non-empty argument.
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// isTruthy reports whether a query flag means "on".
func isTruthy(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// setStr sets key only when v is non-empty.
func setStr(m map[string]any, key, v string) {
	if v != "" {
		m[key] = v
	}
}

// transportKind mirrors core's transport selection so the clash/surge mappers
// stay consistent with the sing-box output: vmess carries the network in `net`
// (its `type` is a header-obfs hint), every other protocol in `type` then `net`.
func transportKind(s model.Server) string {
	if s.Protocol == model.ProtocolVMess {
		return strings.ToLower(s.Params["net"])
	}
	return strings.ToLower(orDefault(s.Params["type"], s.Params["net"]))
}

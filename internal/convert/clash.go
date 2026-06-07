package convert

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/whitedns/vless-tester/internal/model"
)

// clashGroup is the manual proxy-group name used by the surge renderer.
const clashGroup = "WhiteDNS"

// clashTemplate is the ACL4SSR full template (proxy-groups, rule-providers and
// rules) that WhiteDNS ships, with no proxies. Its country/Netflix groups use
// `include-all: true` + a name `filter:` regex, so they self-populate from the
// injected proxies (whose names carry the country code, flag and media tags).
//
//go:embed templates/clash_acl4ssr.yaml
var clashTemplate []byte

// renderClash emits a clash-meta (mihomo) config: the embedded ACL4SSR template
// with our proxies injected. Map keys serialize sorted, so output is
// deterministic for a given input.
func renderClash(nodes []Node) ([]byte, error) {
	proxies := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		if p := clashProxy(n); p != nil {
			proxies = append(proxies, p)
		}
	}
	var doc map[string]any
	if err := yaml.Unmarshal(clashTemplate, &doc); err != nil {
		return nil, fmt.Errorf("convert: parse clash template: %w", err)
	}
	doc["proxies"] = proxies
	return yaml.Marshal(doc)
}

// clashProxy maps a node to a mihomo proxy entry, or nil when the protocol has
// no clash representation.
func clashProxy(n Node) map[string]any {
	return ClashProxy(n.Server, n.Name)
}

// ClashProxy maps a server to a mihomo (Clash.Meta) proxy entry under the given
// name, or nil when the protocol has no clash representation. It is the single
// source of truth for the server->mihomo mapping, shared by the clash output
// renderer and the in-process testing core (internal/mcore).
func ClashProxy(s model.Server, name string) map[string]any {
	p := map[string]any{"name": name, "server": s.Host, "port": s.Port}
	switch s.Protocol {
	case model.ProtocolShadowsocks:
		p["type"] = "ss"
		p["cipher"] = s.Params["method"]
		p["password"] = s.Credential
	case model.ProtocolVMess:
		p["type"] = "vmess"
		p["uuid"] = s.Credential
		p["alterId"] = atoiZero(s.Params["aid"])
		p["cipher"] = orDefault(s.Params["scy"], "auto")
		if strings.EqualFold(s.Params["tls"], "tls") {
			p["tls"] = true
			setStr(p, "servername", firstNonEmpty(s.Params["sni"], s.Params["host"]))
		}
		clashTransport(p, s)
	case model.ProtocolVLESS:
		p["type"] = "vless"
		p["uuid"] = s.Credential
		setStr(p, "flow", s.Params["flow"])
		switch s.Params["security"] {
		case "tls", "reality":
			p["tls"] = true
			setStr(p, "servername", firstNonEmpty(s.Params["sni"], s.Params["host"]))
			p["client-fingerprint"] = orDefault(s.Params["fp"], "chrome")
			if s.Params["security"] == "reality" {
				p["reality-opts"] = map[string]any{
					"public-key": s.Params["pbk"],
					"short-id":   s.Params["sid"],
				}
			}
		}
		clashTransport(p, s)
	case model.ProtocolTrojan:
		p["type"] = "trojan"
		p["password"] = s.Credential
		setStr(p, "sni", firstNonEmpty(s.Params["sni"], s.Host))
		if isTruthy(s.Params["insecure"]) {
			p["skip-cert-verify"] = true
		}
		clashTransport(p, s)
	case model.ProtocolHysteria2:
		p["type"] = "hysteria2"
		p["password"] = s.Credential
		setStr(p, "sni", firstNonEmpty(s.Params["sni"], s.Host))
		if isTruthy(s.Params["insecure"]) {
			p["skip-cert-verify"] = true
		}
		if obfs := s.Params["obfs"]; obfs != "" {
			p["obfs"] = obfs
			setStr(p, "obfs-password", s.Params["obfs-password"])
		}
	case model.ProtocolTUIC:
		p["type"] = "tuic"
		uuid, password, _ := strings.Cut(s.Credential, ":")
		p["uuid"] = uuid
		p["password"] = password
		setStr(p, "sni", firstNonEmpty(s.Params["sni"], s.Host))
		setStr(p, "congestion-controller", s.Params["congestion_control"])
		if isTruthy(s.Params["insecure"]) {
			p["skip-cert-verify"] = true
		}
	case model.ProtocolSOCKS:
		p["type"] = "socks5"
		if s.Credential != "" {
			user, password, _ := strings.Cut(s.Credential, ":")
			setStr(p, "username", user)
			setStr(p, "password", password)
		}
	case model.ProtocolAnyTLS:
		p["type"] = "anytls"
		p["password"] = s.Credential
		setStr(p, "sni", firstNonEmpty(s.Params["sni"], s.Host))
		if isTruthy(s.Params["insecure"]) {
			p["skip-cert-verify"] = true
		}
	case model.ProtocolHysteria:
		p["type"] = "hysteria"
		setStr(p, "auth-str", s.Credential)
		setStr(p, "up", s.Params["upmbps"])
		setStr(p, "down", s.Params["downmbps"])
		setStr(p, "sni", firstNonEmpty(s.Params["peer"], s.Params["sni"], s.Host))
		setStr(p, "obfs", s.Params["obfs"])
		if isTruthy(s.Params["insecure"]) {
			p["skip-cert-verify"] = true
		}
	default:
		return nil
	}
	return p
}

// clashTransport adds the network and its options for ws/grpc/http transports.
func clashTransport(p map[string]any, s model.Server) {
	switch transportKind(s) {
	case "ws":
		p["network"] = "ws"
		opts := map[string]any{}
		setStr(opts, "path", s.Params["path"])
		if h := s.Params["host"]; h != "" {
			opts["headers"] = map[string]any{"Host": h}
		}
		if len(opts) > 0 {
			p["ws-opts"] = opts
		}
	case "grpc":
		p["network"] = "grpc"
		p["grpc-opts"] = map[string]any{
			"grpc-service-name": orDefault(s.Params["serviceName"], s.Params["path"]),
		}
	case "http", "h2":
		p["network"] = "http"
	}
}

// atoiZero parses an integer, returning 0 on failure.
func atoiZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

package convert

import (
	"strconv"
	"strings"

	"github.com/whitedns/vless-tester/internal/model"
	"gopkg.in/yaml.v3"
)

// clashGroup is the manual proxy-group name every node is placed under.
const clashGroup = "WhiteDNS"

// renderClash emits a clash-meta (mihomo) config with a flat proxies list, a
// single select group and a catch-all rule. Map keys serialize sorted, so the
// output is deterministic for a given input.
func renderClash(nodes []Node) ([]byte, error) {
	proxies := make([]map[string]any, 0, len(nodes))
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		p := clashProxy(n)
		if p == nil {
			continue // protocol not representable in clash; skip it
		}
		proxies = append(proxies, p)
		names = append(names, n.Name)
	}
	groupProxies := names
	if len(groupProxies) == 0 {
		groupProxies = []string{"DIRECT"}
	}
	doc := map[string]any{
		"proxies": proxies,
		"proxy-groups": []map[string]any{{
			"name":    clashGroup,
			"type":    "select",
			"proxies": groupProxies,
		}},
		"rules": []string{"MATCH," + clashGroup},
	}
	return yaml.Marshal(doc)
}

// clashProxy maps a server to a mihomo proxy entry, or nil when the protocol has
// no clash representation.
func clashProxy(n Node) map[string]any {
	s := n.Server
	p := map[string]any{"name": n.Name, "server": s.Host, "port": s.Port}
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

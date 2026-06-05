package convert

import (
	"fmt"
	"strings"

	"github.com/whitedns/vless-tester/internal/model"
)

// renderSurge emits a Surge config: a [Proxy] section, a select [Proxy Group]
// and a catch-all [Rule]. Surge supports only a subset of protocols; nodes it
// cannot represent (vless, anytls, hysteria v1) are skipped.
func renderSurge(nodes []Node) []byte {
	var b strings.Builder
	b.WriteString("[Proxy]\n")
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		line, ok := surgeProxy(n)
		if !ok {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		names = append(names, n.Name)
	}

	b.WriteString("\n[Proxy Group]\n")
	if len(names) == 0 {
		b.WriteString(clashGroup + " = select, DIRECT\n")
	} else {
		b.WriteString(clashGroup + " = select, " + strings.Join(names, ", ") + "\n")
	}

	b.WriteString("\n[Rule]\nFINAL," + clashGroup + "\n")
	return []byte(b.String())
}

// surgeProxy renders one Surge proxy line, or false when the protocol has no
// Surge representation.
func surgeProxy(n Node) (string, bool) {
	s := n.Server
	fields := []string{}
	switch s.Protocol {
	case model.ProtocolShadowsocks:
		fields = append(fields, "ss", s.Host, fmt.Sprint(s.Port),
			"encrypt-method="+s.Params["method"], "password="+s.Credential)
	case model.ProtocolVMess:
		fields = append(fields, "vmess", s.Host, fmt.Sprint(s.Port), "username="+s.Credential)
		if strings.EqualFold(s.Params["tls"], "tls") {
			fields = append(fields, "tls=true")
			fields = appendKV(fields, "sni", firstNonEmpty(s.Params["sni"], s.Params["host"]))
		}
		if transportKind(s) == "ws" {
			fields = append(fields, "ws=true")
			fields = appendKV(fields, "ws-path", s.Params["path"])
			if h := s.Params["host"]; h != "" {
				fields = append(fields, "ws-headers=Host:"+h)
			}
		}
	case model.ProtocolTrojan:
		fields = append(fields, "trojan", s.Host, fmt.Sprint(s.Port), "password="+s.Credential)
		fields = appendKV(fields, "sni", firstNonEmpty(s.Params["sni"], s.Host))
		if isTruthy(s.Params["insecure"]) {
			fields = append(fields, "skip-cert-verify=true")
		}
	case model.ProtocolHysteria2:
		fields = append(fields, "hysteria2", s.Host, fmt.Sprint(s.Port), "password="+s.Credential)
		fields = appendKV(fields, "sni", firstNonEmpty(s.Params["sni"], s.Host))
		if isTruthy(s.Params["insecure"]) {
			fields = append(fields, "skip-cert-verify=true")
		}
	case model.ProtocolTUIC:
		uuid, password, _ := strings.Cut(s.Credential, ":")
		fields = append(fields, "tuic-v5", s.Host, fmt.Sprint(s.Port),
			"uuid="+uuid, "password="+password)
		fields = appendKV(fields, "sni", firstNonEmpty(s.Params["sni"], s.Host))
		if isTruthy(s.Params["insecure"]) {
			fields = append(fields, "skip-cert-verify=true")
		}
	case model.ProtocolSOCKS:
		fields = append(fields, "socks5", s.Host, fmt.Sprint(s.Port))
		if s.Credential != "" {
			user, password, _ := strings.Cut(s.Credential, ":")
			fields = appendKV(fields, "username", user)
			fields = appendKV(fields, "password", password)
		}
	default:
		return "", false
	}
	return n.Name + " = " + strings.Join(fields, ", "), true
}

// appendKV appends "key=value" only when value is non-empty.
func appendKV(fields []string, key, value string) []string {
	if value == "" {
		return fields
	}
	return append(fields, key+"="+value)
}

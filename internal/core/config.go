package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/whitedns/vless-tester/internal/model"
)

// OutboundTag is the routing tag of the proxy-under-test outbound.
const OutboundTag = "proxy"

// BuildConfig renders a minimal sing-box config that exposes a local SOCKS
// inbound on socksPort and routes all traffic through the given server. Tests
// connect to the SOCKS port and measure the proxied path.
func BuildConfig(srv model.Server, socksPort int) ([]byte, error) {
	outbound, err := buildOutbound(srv)
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{
		"log": map[string]any{"disabled": true},
		"inbounds": []any{
			map[string]any{
				"type":        "socks",
				"tag":         "in",
				"listen":      "127.0.0.1",
				"listen_port": socksPort,
			},
		},
		"outbounds": []any{outbound},
		"route":     map[string]any{"final": OutboundTag},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// buildOutbound maps a normalized Server to a sing-box outbound object.
func buildOutbound(srv model.Server) (map[string]any, error) {
	switch srv.Protocol {
	case model.ProtocolVLESS:
		return vlessOutbound(srv), nil
	case model.ProtocolVMess:
		return vmessOutbound(srv), nil
	case model.ProtocolTrojan:
		return trojanOutbound(srv), nil
	case model.ProtocolShadowsocks:
		return shadowsocksOutbound(srv), nil
	case model.ProtocolHysteria2:
		return hysteria2Outbound(srv), nil
	case model.ProtocolTUIC:
		return tuicOutbound(srv), nil
	default:
		return nil, fmt.Errorf("core: unsupported protocol %q", srv.Protocol)
	}
}

func base(srv model.Server, typ string) map[string]any {
	return map[string]any{
		"type":        typ,
		"tag":         OutboundTag,
		"server":      srv.Host,
		"server_port": srv.Port,
	}
}

func vlessOutbound(srv model.Server) map[string]any {
	o := base(srv, "vless")
	o["uuid"] = srv.Credential
	setIf(o, "flow", srv.Params["flow"])
	if tls := buildTLS(srv, srv.Params["security"]); tls != nil {
		o["tls"] = tls
	}
	if tr := buildTransport(srv); tr != nil {
		o["transport"] = tr
	}
	return o
}

func vmessOutbound(srv model.Server) map[string]any {
	o := base(srv, "vmess")
	o["uuid"] = srv.Credential
	o["security"] = orDefault(srv.Params["scy"], "auto")
	o["alter_id"] = atoiOrZero(srv.Params["aid"])
	security := ""
	if strings.EqualFold(srv.Params["tls"], "tls") {
		security = "tls"
	}
	if tls := buildTLS(srv, security); tls != nil {
		o["tls"] = tls
	}
	if tr := buildTransport(srv); tr != nil {
		o["transport"] = tr
	}
	return o
}

func trojanOutbound(srv model.Server) map[string]any {
	o := base(srv, "trojan")
	o["password"] = srv.Credential
	// Trojan mandates TLS; honor an explicit security value but default to tls.
	if tls := buildTLS(srv, orDefault(srv.Params["security"], "tls")); tls != nil {
		o["tls"] = tls
	}
	if tr := buildTransport(srv); tr != nil {
		o["transport"] = tr
	}
	return o
}

func shadowsocksOutbound(srv model.Server) map[string]any {
	o := base(srv, "shadowsocks")
	o["method"] = srv.Params["method"]
	o["password"] = srv.Credential
	return o
}

func hysteria2Outbound(srv model.Server) map[string]any {
	o := base(srv, "hysteria2")
	o["password"] = srv.Credential
	// Hysteria2 always runs over TLS.
	o["tls"] = forceTLS(srv)
	if obfs := srv.Params["obfs"]; obfs != "" {
		o["obfs"] = map[string]any{
			"type":     obfs,
			"password": srv.Params["obfs-password"],
		}
	}
	return o
}

func tuicOutbound(srv model.Server) map[string]any {
	o := base(srv, "tuic")
	uuid, password, _ := strings.Cut(srv.Credential, ":")
	o["uuid"] = uuid
	o["password"] = password
	setIf(o, "congestion_control", srv.Params["congestion_control"])
	setIf(o, "udp_relay_mode", srv.Params["udp_relay_mode"])
	// TUIC always runs over TLS.
	o["tls"] = forceTLS(srv)
	return o
}

// buildTLS returns a sing-box tls object for the given security mode, or nil
// when TLS is not in use.
func buildTLS(srv model.Server, security string) map[string]any {
	switch strings.ToLower(security) {
	case "tls":
		return baseTLS(srv)
	case "reality":
		tls := baseTLS(srv)
		tls["reality"] = map[string]any{
			"enabled":    true,
			"public_key": srv.Params["pbk"],
			"short_id":   srv.Params["sid"],
		}
		// Reality requires a uTLS fingerprint; default to chrome.
		tls["utls"] = map[string]any{
			"enabled":     true,
			"fingerprint": orDefault(srv.Params["fp"], "chrome"),
		}
		return tls
	default:
		return nil
	}
}

// forceTLS builds a tls object for protocols that always run over TLS.
func forceTLS(srv model.Server) map[string]any { return baseTLS(srv) }

func baseTLS(srv model.Server) map[string]any {
	tls := map[string]any{"enabled": true}
	setIf(tls, "server_name", orDefault(srv.Params["sni"], srv.Host))
	if isTruthy(srv.Params["insecure"]) || isTruthy(srv.Params["allowInsecure"]) {
		tls["insecure"] = true
	}
	if alpn := srv.Params["alpn"]; alpn != "" {
		tls["alpn"] = strings.Split(alpn, ",")
	}
	if fp := srv.Params["fp"]; fp != "" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	return tls
}

// buildTransport maps the ws/grpc/http transport hints to a sing-box transport
// object, or nil for plain TCP.
func buildTransport(srv model.Server) map[string]any {
	kind := orDefault(srv.Params["type"], srv.Params["net"])
	host := srv.Params["host"]
	path := srv.Params["path"]
	switch strings.ToLower(kind) {
	case "ws":
		tr := map[string]any{"type": "ws"}
		setIf(tr, "path", path)
		if host != "" {
			tr["headers"] = map[string]any{"Host": host}
		}
		return tr
	case "grpc":
		return map[string]any{
			"type":         "grpc",
			"service_name": orDefault(srv.Params["serviceName"], path),
		}
	case "http", "h2":
		tr := map[string]any{"type": "http"}
		setIf(tr, "path", path)
		if host != "" {
			tr["host"] = []string{host}
		}
		return tr
	default:
		return nil
	}
}

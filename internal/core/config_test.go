package core_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/whitedns/vless-tester/internal/core"
	"github.com/whitedns/vless-tester/internal/model"
)

func decode(t *testing.T, srv model.Server, port int) map[string]any {
	t.Helper()
	raw, err := core.BuildConfig(srv, port)
	if err != nil {
		t.Fatalf("BuildConfig(%s): %v", srv.Protocol, err)
	}
	if !json.Valid(raw) {
		t.Fatalf("BuildConfig(%s) produced invalid json", srv.Protocol)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return cfg
}

// outbound returns the single proxy outbound from a decoded config.
func outbound(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	outs, ok := cfg["outbounds"].([]any)
	if !ok || len(outs) != 1 {
		t.Fatalf("expected exactly one outbound, got %v", cfg["outbounds"])
	}
	return outs[0].(map[string]any)
}

func TestBuildConfigInboundAndRoute(t *testing.T) {
	srv := model.Server{Protocol: model.ProtocolShadowsocks, Host: "h", Port: 8388, Credential: "pw", Params: map[string]string{"method": "aes-256-gcm"}}
	cfg := decode(t, srv, 10808)

	ins := cfg["inbounds"].([]any)
	in0 := ins[0].(map[string]any)
	if in0["type"] != "socks" {
		t.Errorf("inbound type = %v, want socks", in0["type"])
	}
	if in0["listen_port"].(float64) != 10808 {
		t.Errorf("listen_port = %v, want 10808", in0["listen_port"])
	}
	route := cfg["route"].(map[string]any)
	if route["final"] != core.OutboundTag {
		t.Errorf("route.final = %v, want %s", route["final"], core.OutboundTag)
	}
}

func TestBuildConfigPerProtocol(t *testing.T) {
	tests := []struct {
		name       string
		srv        model.Server
		wantType   string
		credKey    string // outbound key carrying the credential
		credValue  string
		wantTLS    bool
		wantTransp string // empty = none
	}{
		{
			name: "vless-reality-vision",
			srv: model.Server{Protocol: model.ProtocolVLESS, Host: "v.example", Port: 443, Credential: "uuid-1",
				Params: map[string]string{"security": "reality", "pbk": "KEY", "sid": "01", "sni": "v.example", "flow": "xtls-rprx-vision"}},
			wantType: "vless", credKey: "uuid", credValue: "uuid-1", wantTLS: true,
		},
		{
			name: "vless-ws-tls",
			srv: model.Server{Protocol: model.ProtocolVLESS, Host: "v.example", Port: 443, Credential: "uuid-2",
				Params: map[string]string{"security": "tls", "type": "ws", "path": "/ws", "host": "cdn.example", "sni": "v.example"}},
			wantType: "vless", credKey: "uuid", credValue: "uuid-2", wantTLS: true, wantTransp: "ws",
		},
		{
			name: "vmess-ws-tls",
			srv: model.Server{Protocol: model.ProtocolVMess, Host: "m.example", Port: 443, Credential: "uuid-3",
				Params: map[string]string{"net": "ws", "tls": "tls", "path": "/r", "host": "cdn.example", "scy": "auto", "aid": "0"}},
			wantType: "vmess", credKey: "uuid", credValue: "uuid-3", wantTLS: true, wantTransp: "ws",
		},
		{
			name: "trojan-grpc",
			srv: model.Server{Protocol: model.ProtocolTrojan, Host: "t.example", Port: 443, Credential: "secret",
				Params: map[string]string{"sni": "t.example", "type": "grpc", "serviceName": "grpcsvc"}},
			wantType: "trojan", credKey: "password", credValue: "secret", wantTLS: true, wantTransp: "grpc",
		},
		{
			name: "shadowsocks",
			srv: model.Server{Protocol: model.ProtocolShadowsocks, Host: "s.example", Port: 8388, Credential: "pw",
				Params: map[string]string{"method": "aes-256-gcm"}},
			wantType: "shadowsocks", credKey: "password", credValue: "pw", wantTLS: false,
		},
		{
			name: "hysteria2",
			srv: model.Server{Protocol: model.ProtocolHysteria2, Host: "h.example", Port: 443, Credential: "pw2",
				Params: map[string]string{"sni": "h.example", "insecure": "1"}},
			wantType: "hysteria2", credKey: "password", credValue: "pw2", wantTLS: true,
		},
		{
			name: "tuic",
			srv: model.Server{Protocol: model.ProtocolTUIC, Host: "u.example", Port: 443, Credential: "uuid-4:pw3",
				Params: map[string]string{"congestion_control": "bbr", "sni": "u.example"}},
			wantType: "tuic", credKey: "uuid", credValue: "uuid-4", wantTLS: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := decode(t, tt.srv, 10808)
			out := outbound(t, cfg)

			if out["type"] != tt.wantType {
				t.Errorf("type = %v, want %s", out["type"], tt.wantType)
			}
			if out["server"] != tt.srv.Host {
				t.Errorf("server = %v, want %s", out["server"], tt.srv.Host)
			}
			if out["server_port"].(float64) != float64(tt.srv.Port) {
				t.Errorf("server_port = %v, want %d", out["server_port"], tt.srv.Port)
			}
			if out[tt.credKey] != tt.credValue {
				t.Errorf("%s = %v, want %s", tt.credKey, out[tt.credKey], tt.credValue)
			}

			tls, hasTLS := out["tls"].(map[string]any)
			if tt.wantTLS {
				if !hasTLS || tls["enabled"] != true {
					t.Errorf("expected tls.enabled=true, got %v", out["tls"])
				}
			} else if hasTLS {
				t.Errorf("expected no tls block, got %v", tls)
			}

			if tt.wantTransp != "" {
				tr, ok := out["transport"].(map[string]any)
				if !ok || tr["type"] != tt.wantTransp {
					t.Errorf("transport = %v, want type %s", out["transport"], tt.wantTransp)
				}
			} else if _, ok := out["transport"]; ok {
				t.Errorf("expected no transport, got %v", out["transport"])
			}
		})
	}
}

func TestBuildConfigReality(t *testing.T) {
	srv := model.Server{Protocol: model.ProtocolVLESS, Host: "v.example", Port: 443, Credential: "uuid",
		Params: map[string]string{"security": "reality", "pbk": "PUBKEY", "sid": "ab", "fp": "chrome"}}
	out := outbound(t, decode(t, srv, 1080))
	tls := out["tls"].(map[string]any)
	reality, ok := tls["reality"].(map[string]any)
	if !ok || reality["enabled"] != true || reality["public_key"] != "PUBKEY" {
		t.Fatalf("reality block missing/incorrect: %v", tls["reality"])
	}
}

func TestBuildConfigDeterministic(t *testing.T) {
	srv := model.Server{Protocol: model.ProtocolVLESS, Host: "v.example", Port: 443, Credential: "uuid",
		Params: map[string]string{"security": "tls", "type": "ws", "path": "/p", "host": "h", "sni": "x"}}
	a, _ := core.BuildConfig(srv, 1080)
	b, _ := core.BuildConfig(srv, 1080)
	if !bytes.Equal(a, b) {
		t.Fatal("BuildConfig is not deterministic")
	}
}

func TestBuildConfigUnsupported(t *testing.T) {
	srv := model.Server{Protocol: model.Protocol("wireguard"), Host: "h", Port: 1}
	if _, err := core.BuildConfig(srv, 1080); err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}

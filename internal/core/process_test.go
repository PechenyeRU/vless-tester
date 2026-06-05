package core_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/whitedns/vless-tester/internal/core"
	"github.com/whitedns/vless-tester/internal/model"
)

func TestResolveBinaryExplicit(t *testing.T) {
	got, err := core.ResolveBinary("/opt/sing-box")
	if err != nil || got != "/opt/sing-box" {
		t.Fatalf("ResolveBinary(explicit) = %q, %v", got, err)
	}
}

func TestResolveBinaryEnv(t *testing.T) {
	t.Setenv("SINGBOX_BIN", "/custom/sing-box")
	got, err := core.ResolveBinary("")
	if err != nil || got != "/custom/sing-box" {
		t.Fatalf("ResolveBinary(env) = %q, %v", got, err)
	}
}

func TestResolveBinaryNotFound(t *testing.T) {
	t.Setenv("SINGBOX_BIN", "")
	if _, err := exec.LookPath("sing-box"); err == nil {
		t.Skip("sing-box present in PATH; cannot test not-found branch")
	}
	if _, err := core.ResolveBinary(""); !errors.Is(err, core.ErrBinaryNotFound) {
		t.Fatalf("expected ErrBinaryNotFound, got %v", err)
	}
}

func TestFreePort(t *testing.T) {
	port, err := core.FreePort()
	if err != nil {
		t.Fatalf("FreePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("FreePort returned %d", port)
	}
	// The port must be immediately bindable.
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("returned port not bindable: %v", err)
	}
	l.Close()
}

// TestStartAndClose is gated: it only runs when a sing-box binary is available.
// The outbound points at a non-routable address; sing-box still opens the local
// SOCKS inbound, which is what we assert.
func TestStartAndClose(t *testing.T) {
	if _, err := core.ResolveBinary(""); err != nil {
		t.Skip("sing-box not available; skipping spawn test")
	}
	srv := model.Server{
		Protocol:   model.ProtocolShadowsocks,
		Host:       "192.0.2.1", // TEST-NET-1, never routable
		Port:       8388,
		Credential: "password",
		Params:     map[string]string{"method": "aes-256-gcm"},
	}
	ctx := context.Background()
	inst, err := core.Start(ctx, srv, core.Options{StartTimeout: 8 * time.Second})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer inst.Close()

	conn, err := net.DialTimeout("tcp", inst.SocksAddress(), 2*time.Second)
	if err != nil {
		t.Fatalf("SOCKS port not accepting: %v", err)
	}
	conn.Close()
}

// TestConfigsPassSingboxCheck validates every protocol's generated config
// against the real sing-box schema via `sing-box check`. Gated: skipped when no
// binary is available.
func TestConfigsPassSingboxCheck(t *testing.T) {
	bin, err := core.ResolveBinary("")
	if err != nil {
		t.Skip("sing-box not available; skipping schema validation")
	}

	servers := map[string]model.Server{
		// pbk is a valid base64url-encoded 32-byte x25519 key (sing-box validates it).
		"vless-reality": {Protocol: model.ProtocolVLESS, Host: "v.example", Port: 443, Credential: "11111111-1111-1111-1111-111111111111",
			Params: map[string]string{"security": "reality", "pbk": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "sid": "ab", "flow": "xtls-rprx-vision", "fp": "chrome"}},
		"vless-ws-tls": {Protocol: model.ProtocolVLESS, Host: "v.example", Port: 443, Credential: "11111111-1111-1111-1111-111111111111",
			Params: map[string]string{"security": "tls", "type": "ws", "path": "/ws", "host": "cdn.example", "sni": "v.example"}},
		"vmess-ws-tls": {Protocol: model.ProtocolVMess, Host: "m.example", Port: 443, Credential: "22222222-2222-2222-2222-222222222222",
			Params: map[string]string{"net": "ws", "tls": "tls", "path": "/r", "host": "cdn.example", "scy": "auto", "aid": "0"}},
		"trojan-grpc": {Protocol: model.ProtocolTrojan, Host: "t.example", Port: 443, Credential: "secret",
			Params: map[string]string{"sni": "t.example", "type": "grpc", "serviceName": "grpcsvc"}},
		"shadowsocks": {Protocol: model.ProtocolShadowsocks, Host: "s.example", Port: 8388, Credential: "password",
			Params: map[string]string{"method": "aes-256-gcm"}},
		"hysteria2": {Protocol: model.ProtocolHysteria2, Host: "h.example", Port: 443, Credential: "pw",
			Params: map[string]string{"sni": "h.example", "insecure": "1"}},
		"tuic": {Protocol: model.ProtocolTUIC, Host: "u.example", Port: 443, Credential: "33333333-3333-3333-3333-333333333333:pw",
			Params: map[string]string{"congestion_control": "bbr", "sni": "u.example", "alpn": "h3"}},
	}

	for name, srv := range servers {
		t.Run(name, func(t *testing.T) {
			cfg, err := core.BuildConfig(srv, 10808)
			if err != nil {
				t.Fatalf("BuildConfig: %v", err)
			}
			path := filepath.Join(t.TempDir(), name+".json")
			if err := os.WriteFile(path, cfg, 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			out, err := exec.Command(bin, "check", "-c", path).CombinedOutput()
			if err != nil {
				t.Fatalf("sing-box check failed: %v\n%s\nconfig:\n%s", err, out, cfg)
			}
		})
	}
}

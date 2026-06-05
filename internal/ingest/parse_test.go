package ingest

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/whitedns/vless-tester/internal/model"
)

const sampleUUID = "b831381d-6324-4d53-ad4f-8cda48b30811"

// vmessLink builds a base64-wrapped vmess link from a JSON body.
func vmessLink(jsonBody string) string {
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(jsonBody))
}

// ssSIP002 builds a SIP002 shadowsocks link: ss://base64(method:pass)@host:port.
func ssSIP002(method, pass, host string, port int, name string) string {
	cred := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + pass))
	return "ss://" + cred + "@" + host + ":" + strconv.Itoa(port) + "#" + name
}

// ssLegacy builds the fully-encoded form: ss://base64(method:pass@host:port).
func ssLegacy(method, pass, host string, port int, name string) string {
	body := base64.StdEncoding.EncodeToString([]byte(method + ":" + pass + "@" + host + ":" + strconv.Itoa(port)))
	return "ss://" + body + "#" + name
}

func TestParseProtocols(t *testing.T) {
	vmessBody := `{"v":"2","ps":"DE-1","add":"vmess.example.com","port":"443","id":"` + sampleUUID +
		`","aid":"0","net":"ws","type":"none","host":"vmess.example.com","path":"/ray","tls":"tls","scy":"auto"}`

	tests := []struct {
		name  string
		raw   string
		proto model.Protocol
		host  string
		port  int
	}{
		{"vless", "vless://" + sampleUUID + "@vless.example.com:443?type=ws&security=tls&sni=vless.example.com&path=%2Fws#FR-1", model.ProtocolVLESS, "vless.example.com", 443},
		{"vmess", vmessLink(vmessBody), model.ProtocolVMess, "vmess.example.com", 443},
		{"trojan", "trojan://pass123@trojan.example.com:8443?sni=trojan.example.com&type=tcp#TR", model.ProtocolTrojan, "trojan.example.com", 8443},
		{"tuic", "tuic://" + sampleUUID + ":secret@tuic.example.com:443?congestion_control=bbr&sni=tuic.example.com#TU", model.ProtocolTUIC, "tuic.example.com", 443},
		{"hysteria2", "hysteria2://auth123@hy2.example.com:443?sni=hy2.example.com&insecure=1#HY", model.ProtocolHysteria2, "hy2.example.com", 443},
		{"hy2-alias", "hy2://auth123@hy2.example.com:8443?sni=hy2.example.com#HY", model.ProtocolHysteria2, "hy2.example.com", 8443},
		{"ss-sip002", ssSIP002("aes-256-gcm", "password", "ss.example.com", 8388, "SS"), model.ProtocolShadowsocks, "ss.example.com", 8388},
		{"ss-legacy", ssLegacy("aes-256-gcm", "password", "ss.example.com", 8388, "SS"), model.ProtocolShadowsocks, "ss.example.com", 8388},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse(%s) error: %v", tt.name, err)
			}
			if srv.Protocol != tt.proto {
				t.Errorf("protocol = %s, want %s", srv.Protocol, tt.proto)
			}
			if srv.Host != tt.host {
				t.Errorf("host = %s, want %s", srv.Host, tt.host)
			}
			if srv.Port != tt.port {
				t.Errorf("port = %d, want %d", srv.Port, tt.port)
			}
			if srv.Fingerprint == "" || len(srv.Fingerprint) != 64 {
				t.Errorf("fingerprint = %q, want 64-hex-char digest", srv.Fingerprint)
			}
			if srv.RawURI != tt.raw {
				t.Errorf("raw uri not preserved")
			}
		})
	}
}

func TestShadowsocksFormsMatch(t *testing.T) {
	a, err := Parse(ssSIP002("aes-256-gcm", "password", "ss.example.com", 8388, "name-A"))
	if err != nil {
		t.Fatalf("sip002: %v", err)
	}
	b, err := Parse(ssLegacy("aes-256-gcm", "password", "ss.example.com", 8388, "name-B"))
	if err != nil {
		t.Fatalf("legacy: %v", err)
	}
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("ss forms should fingerprint equal: %s != %s", a.Fingerprint, b.Fingerprint)
	}
}

func TestFingerprintIgnoresName(t *testing.T) {
	base := "vless://" + sampleUUID + "@example.com:443?type=ws&security=tls&sni=example.com"
	a, _ := Parse(base + "#Name-One")
	b, _ := Parse(base + "#Completely-Different")
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("fingerprint must ignore the #name fragment")
	}
}

func TestFingerprintSensitiveToParams(t *testing.T) {
	a, _ := Parse("vless://" + sampleUUID + "@example.com:443?type=ws&sni=a.example.com#x")
	b, _ := Parse("vless://" + sampleUUID + "@example.com:443?type=ws&sni=b.example.com#x")
	if a.Fingerprint == b.Fingerprint {
		t.Fatalf("different sni must produce different fingerprints")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []string{
		"http://example.com",                           // unsupported scheme
		"not-a-link",                                   // no scheme
		"vless://" + sampleUUID + "@:443",              // empty host
		"vless://" + sampleUUID + "@example.com:0",     // invalid port
		"vless://" + sampleUUID + "@example.com:99999", // out-of-range port
		"vmess://!!!not-base64!!!",                     // bad vmess payload
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", raw)
		}
	}
}

func TestParseUnsupportedSentinel(t *testing.T) {
	_, err := Parse("ftp://example.com")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestDedup(t *testing.T) {
	link := "vless://" + sampleUUID + "@example.com:443?type=ws&sni=example.com"
	servers, failed := ParseList(strings.Join([]string{
		link + "#a",
		link + "#b", // duplicate endpoint, different name
		"trojan://pw@t.example.com:443#t",
		"# a comment line",
		"",
		"garbage://line",
	}, "\n"))
	if len(failed) != 1 {
		t.Fatalf("failed lines = %v, want exactly 1 (the garbage scheme)", failed)
	}
	if len(servers) != 3 {
		t.Fatalf("parsed = %d, want 3", len(servers))
	}
	unique, dropped := Dedup(servers)
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(unique) != 2 {
		t.Fatalf("unique = %d, want 2", len(unique))
	}
}

func TestParseSubscriptionBase64(t *testing.T) {
	links := "vless://" + sampleUUID + "@example.com:443#a\ntrojan://pw@t.example.com:443#b"
	encoded := base64.StdEncoding.EncodeToString([]byte(links))
	servers, failed := ParseSubscription(encoded)
	if len(failed) != 0 {
		t.Fatalf("failed = %v, want none", failed)
	}
	if len(servers) != 2 {
		t.Fatalf("servers = %d, want 2", len(servers))
	}
}

func TestParseSubscriptionPlain(t *testing.T) {
	links := "vless://" + sampleUUID + "@example.com:443#a\ntrojan://pw@t.example.com:443#b"
	servers, _ := ParseSubscription(links)
	if len(servers) != 2 {
		t.Fatalf("plain subscription servers = %d, want 2", len(servers))
	}
}

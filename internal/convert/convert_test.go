package convert

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whitedns/vless-tester/internal/ingest"
	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "rewrite golden files")

// sampleURIs is one share link per supported protocol, mirroring the ingest
// parser test corpus, plus a reality vless to exercise reality-opts. The display
// names are deterministic so the golden output is stable.
var sampleURIs = []struct {
	name string
	uri  string
}{
	{"VLESS", "vless://11111111-1111-1111-1111-111111111111@vless.example.com:443?type=ws&security=tls&sni=vless.example.com&path=%2Fws&host=cdn.example.com#ignored"},
	{"VLESS-reality", "vless://11111111-1111-1111-1111-111111111111@reality.example.com:443?type=grpc&security=reality&sni=reality.example.com&pbk=PUBKEY&sid=abcd&fp=chrome&flow=xtls-rprx-vision&serviceName=grpcsvc#ignored"},
	{"VMess", vmessLink(`{"v":"2","ps":"ignored","add":"vmess.example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","aid":"0","net":"ws","type":"none","host":"cdn.example.com","path":"/ray","tls":"tls","scy":"auto","sni":"vmess.example.com"}`)},
	{"Trojan", "trojan://pass123@trojan.example.com:8443?sni=trojan.example.com&type=tcp&insecure=1#ignored"},
	{"TUIC", "tuic://11111111-1111-1111-1111-111111111111:secret@tuic.example.com:443?congestion_control=bbr&sni=tuic.example.com#ignored"},
	{"Hysteria2", "hysteria2://auth123@hy2.example.com:443?sni=hy2.example.com&insecure=1&obfs=salamander&obfs-password=obfspw#ignored"},
	{"Shadowsocks", "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:password")) + "@ss.example.com:8388#ignored"},
	{"AnyTLS", "anytls://password@at.example.com:443?sni=at.example.com&insecure=1#ignored"},
	{"HysteriaV1", "hysteria://h1.example.com:443?auth=secret&peer=h1.example.com&upmbps=100&downmbps=50&obfs=xplus&insecure=1#ignored"},
	{"SOCKS", "socks5://user:pass@sk.example.com:1080#ignored"},
}

// sampleNodes parses every sample URI and assigns a stable public name.
func sampleNodes(t *testing.T) []Node {
	t.Helper()
	nodes := make([]Node, 0, len(sampleURIs))
	for _, s := range sampleURIs {
		srv, err := ingest.Parse(s.uri)
		if err != nil {
			t.Fatalf("parse %s: %v", s.name, err)
		}
		nodes = append(nodes, Node{Server: srv, Name: "🇫🇷 | @WhiteDNS | " + s.name})
	}
	return nodes
}

// TestRenderGolden renders the full sample set into every target and compares
// against the checked-in golden files. Run with -update to regenerate them.
func TestRenderGolden(t *testing.T) {
	nodes := sampleNodes(t)
	for _, target := range Targets {
		t.Run(target, func(t *testing.T) {
			got, err := Render(target, nodes)
			if err != nil {
				t.Fatalf("render %s: %v", target, err)
			}
			golden := filepath.Join("testdata", target+".golden")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run -update first): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s output mismatch:\n--- got ---\n%s\n--- want ---\n%s", target, got, want)
			}
		})
	}
}

// TestClashParses asserts the clash output is valid, parseable yaml carrying one
// proxy per representable protocol (the DoD: GET /sub?target=clash yields valid
// yaml).
func TestClashParses(t *testing.T) {
	out, err := Render(TargetClash, sampleNodes(t))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies      []map[string]any `yaml:"proxies"`
		ProxyGroups  []map[string]any `yaml:"proxy-groups"`
		RuleProvider map[string]any   `yaml:"rule-providers"`
		Rules        []string         `yaml:"rules"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("clash yaml does not parse: %v", err)
	}
	// All 10 samples are representable in clash-meta.
	if len(doc.Proxies) != len(sampleURIs) {
		t.Errorf("proxies = %d, want %d", len(doc.Proxies), len(sampleURIs))
	}
	// The ACL4SSR template contributes many groups, rule-providers and rules.
	if len(doc.ProxyGroups) < 5 || len(doc.RuleProvider) == 0 || len(doc.Rules) == 0 {
		t.Errorf("template not applied: groups=%d providers=%d rules=%d",
			len(doc.ProxyGroups), len(doc.RuleProvider), len(doc.Rules))
	}
	// A known ACL4SSR group must be present.
	var hasSelect bool
	for _, g := range doc.ProxyGroups {
		if name, _ := g["name"].(string); strings.Contains(name, "Proxy Select") {
			hasSelect = true
		}
	}
	if !hasSelect {
		t.Errorf("expected a 'Proxy Select' group from the ACL4SSR template")
	}
}

// TestSingboxParses asserts the sing-box output is valid JSON with one outbound
// per node plus the auto/select/direct trio.
func TestSingboxParses(t *testing.T) {
	out, err := Render(TargetSingbox, sampleNodes(t))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("singbox json does not parse: %v", err)
	}
	if len(cfg.Outbounds) != len(sampleURIs)+3 {
		t.Errorf("outbounds = %d, want %d", len(cfg.Outbounds), len(sampleURIs)+3)
	}
}

// TestBase64RoundTrips asserts the base64 form decodes back to the v2ray URI
// list and every node name survives the rename.
func TestBase64RoundTrips(t *testing.T) {
	nodes := sampleNodes(t)
	b64, _ := Render(TargetBase64, nodes)
	raw, err := base64.StdEncoding.DecodeString(string(b64))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	v2ray, _ := Render(TargetV2ray, nodes)
	if !bytes.Equal(raw, v2ray) {
		t.Errorf("base64 does not decode to the v2ray list")
	}
	lines := strings.Split(string(v2ray), "\n")
	if len(lines) != len(nodes) {
		t.Fatalf("v2ray lines = %d, want %d", len(lines), len(nodes))
	}
	for i, n := range nodes {
		if !strings.Contains(lines[i], n.Name) && !strings.HasPrefix(lines[i], "vmess://") {
			t.Errorf("line %d missing node name %q: %s", i, n.Name, lines[i])
		}
	}
}

// TestNoInnerWorkingLeak asserts no diagnostic/vantage token appears in any
// rendered format. Node names are public; everything else derives from the
// public share URI.
func TestNoInnerWorkingLeak(t *testing.T) {
	nodes := sampleNodes(t)
	// Inner-working terms that must never reach a public subscription. ("client-
	// fingerprint" is an excluded legitimate clash uTLS field, so "fingerprint"
	// is not banned wholesale.)
	banned := []string{"worker", "vantage", "batch", "heartbeat", "claim", "lease"}
	for _, target := range Targets {
		out, err := Render(target, nodes)
		if err != nil {
			t.Fatal(err)
		}
		low := strings.ToLower(string(out))
		for _, w := range banned {
			if strings.Contains(low, w) {
				t.Errorf("%s output leaks %q", target, w)
			}
		}
	}
}

func vmessLink(jsonBody string) string {
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(jsonBody))
}

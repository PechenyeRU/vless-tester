package output_test

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/output"
)

func sampleServers() []output.PublicServer {
	return []output.PublicServer{
		{RawURI: "vless://uuid@fr.example.com:443?type=ws#old-name", Country: "FR", SeqName: "FR110", SpeedMBps: 12.34},
		{RawURI: "trojan://pw@ch.example.com:443#whatever", Country: "CH", SeqName: "CH23", SpeedMBps: 8.0},
	}
}

func TestNodeName(t *testing.T) {
	s := output.PublicServer{Country: "FR", SeqName: "FR1", SpeedMBps: 12.34, Tags: []string{"GPT⁺-FR", "GM-FR"}}
	got := output.NodeName("@WhiteDNS", "", s)
	want := "🇫🇷 | @WhiteDNS | FR1|12.3MB/s|GPT⁺-FR|GM-FR"
	if got != want {
		t.Fatalf("NodeName = %q, want %q", got, want)
	}
}

func TestNodeNamePrefix(t *testing.T) {
	s := output.PublicServer{Country: "FR", SeqName: "FR1", SpeedMBps: 5}
	got := output.NodeName("@WhiteDNS", "🔥 ", s)
	if got != "🔥 🇫🇷 | @WhiteDNS | FR1|5.0MB/s" {
		t.Fatalf("prefixed name = %q", got)
	}
}

func TestNodeNameSpeedUnit(t *testing.T) {
	// Below 1 MB/s renders KB/s (integer), matching the WhiteDNS format.
	slow := output.NodeName("@WhiteDNS", "", output.PublicServer{Country: "DE", SeqName: "DE1", SpeedMBps: 0.953})
	if slow != "🇩🇪 | @WhiteDNS | DE1|953KB/s" {
		t.Fatalf("slow node name = %q", slow)
	}
}

func TestNodeNameUnknownCountry(t *testing.T) {
	s := output.PublicServer{Country: "", SeqName: "OT1", SpeedMBps: 1.0}
	got := output.NodeName("", "", s)
	if !strings.Contains(got, "@WhiteDNS") || !strings.HasPrefix(got, "❓") {
		t.Fatalf("unexpected fallback node name: %q", got)
	}
}

func TestMediaTags(t *testing.T) {
	checks := []model.CheckOutcome{
		{Name: "openai", Passed: true, Detail: "US"},
		{Name: "gemini", Passed: true, Detail: "available"}, // non-region detail -> node country
		{Name: "netflix", Passed: false, Detail: "blocked"}, // failed -> skipped
		{Name: "ip_risk", Passed: false, Detail: "proxy"},   // never a media tag
		{Name: "spotify", Passed: true, Detail: "DE"},
	}
	got := output.MediaTags("FR", checks)
	want := []string{"GPT⁺-US", "GM-FR", "SP-DE"} // order: openai, gemini, spotify
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}
}

func TestBuildArtifactsSubscription(t *testing.T) {
	files, err := output.BuildArtifacts(sampleServers(), output.Options{})
	if err != nil {
		t.Fatalf("BuildArtifacts: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(files[output.FileSubscription]))
	if err != nil {
		t.Fatalf("subscription not valid base64: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(decoded)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d links, want 2", len(lines))
	}
	// The first link must be renamed with the node name in its fragment,
	// percent-encoded so strict clients accept it.
	if !strings.Contains(lines[0], "#"+url.PathEscape("🇫🇷 | @WhiteDNS | FR110|12.3MB/s")) {
		t.Fatalf("first link not renamed: %q", lines[0])
	}
	// Connection part must be preserved.
	if !strings.HasPrefix(lines[0], "vless://uuid@fr.example.com:443?type=ws") {
		t.Fatalf("connection params lost: %q", lines[0])
	}
}

func TestRenameVMessPreservesConnection(t *testing.T) {
	body := `{"v":"2","ps":"old","add":"m.example.com","port":"443","id":"the-uuid","net":"ws","tls":"tls"}`
	raw := "vmess://" + base64.StdEncoding.EncodeToString([]byte(body))
	servers := []output.PublicServer{{RawURI: raw, Country: "DE", SeqName: "DE5", SpeedMBps: 5.5}}

	files, err := output.BuildArtifacts(servers, output.Options{})
	if err != nil {
		t.Fatalf("BuildArtifacts: %v", err)
	}
	decoded, _ := base64.StdEncoding.DecodeString(string(files[output.FileSubscription]))
	link := strings.TrimSpace(string(decoded))
	if !strings.HasPrefix(link, "vmess://") {
		t.Fatalf("not a vmess link: %q", link)
	}
	payload := strings.TrimPrefix(link, "vmess://")
	jsonBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("vmess payload not base64: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(jsonBytes, &obj); err != nil {
		t.Fatalf("vmess payload not json: %v", err)
	}
	if obj["ps"] != "🇩🇪 | @WhiteDNS | DE5|5.5MB/s" {
		t.Fatalf("ps not renamed: %v", obj["ps"])
	}
	if obj["id"] != "the-uuid" || obj["add"] != "m.example.com" {
		t.Fatalf("connection fields altered: %v", obj)
	}
}

// TestPublicArtifactsLeakNoInnerWorking guards the OSINT requirement: the public
// JSON and README must not expose worker/vantage/diagnostic details.
func TestPublicArtifactsLeakNoInnerWorking(t *testing.T) {
	files, err := output.BuildArtifacts(sampleServers(), output.Options{})
	if err != nil {
		t.Fatalf("BuildArtifacts: %v", err)
	}
	forbidden := []string{"worker", "vantage", "latency", "claimed", "run_at", "error", "fingerprint"}
	for _, name := range []string{output.FileJSON, output.FileReadme} {
		lower := strings.ToLower(string(files[name]))
		for _, token := range forbidden {
			if strings.Contains(lower, token) {
				t.Errorf("%s leaks inner-working token %q", name, token)
			}
		}
	}
}

func TestBuildReadmeDeterministic(t *testing.T) {
	a, _ := output.BuildArtifacts(sampleServers(), output.Options{})
	b, _ := output.BuildArtifacts(sampleServers(), output.Options{})
	if string(a[output.FileReadme]) != string(b[output.FileReadme]) {
		t.Fatal("README generation is not deterministic")
	}
}

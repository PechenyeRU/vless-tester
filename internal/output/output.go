package output

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/whitedns/vless-tester/internal/naming"
)

// Artifact file names published to the public repository.
const (
	FileSubscription = "working.txt"
	FileJSON         = "working.json"
	FileReadme       = "README.md"
)

// DefaultBrand is the tag injected into node names.
const DefaultBrand = "@WhiteDNS"

// unknownFlag is used when the country could not be determined.
const unknownFlag = "🌐"

// PublicServer is the public, leak-free view of an approved server. It carries
// only what users and the README need; no worker, vantage or diagnostic data.
type PublicServer struct {
	RawURI    string
	Country   string
	SeqName   string
	SpeedMBps float64
}

// Options tunes artifact generation.
type Options struct {
	Brand string // defaults to DefaultBrand
}

// NodeName renders the display name, e.g. "🇫🇷 | @WhiteDNS | FR110 | 12.3 MB/s".
func NodeName(brand string, s PublicServer) string {
	if brand == "" {
		brand = DefaultBrand
	}
	flag := naming.Emoji(s.Country)
	if flag == "" {
		flag = unknownFlag
	}
	return strings.Join([]string{
		flag,
		brand,
		s.SeqName,
		fmt.Sprintf("%.1f MB/s", s.SpeedMBps),
	}, " | ")
}

// publicRecord is the JSON shape published in working.json. Its fields are
// deliberately limited to public information.
type publicRecord struct {
	Emoji     string  `json:"emoji"`
	Country   string  `json:"country"`
	SeqName   string  `json:"seq_name"`
	Name      string  `json:"name"`
	SpeedMBps float64 `json:"speed_mbps"`
}

// BuildArtifacts renders the subscription, JSON and README from approved
// servers. The returned map is keyed by file name.
func BuildArtifacts(servers []PublicServer, opts Options) (map[string][]byte, error) {
	brand := opts.Brand
	if brand == "" {
		brand = DefaultBrand
	}

	links := make([]string, 0, len(servers))
	records := make([]publicRecord, 0, len(servers))
	for _, s := range servers {
		name := NodeName(brand, s)
		links = append(links, renameLink(s.RawURI, name))
		flag := naming.Emoji(s.Country)
		if flag == "" {
			flag = unknownFlag
		}
		records = append(records, publicRecord{
			Emoji:     flag,
			Country:   s.Country,
			SeqName:   s.SeqName,
			Name:      name,
			SpeedMBps: s.SpeedMBps,
		})
	}

	sub := base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))

	jsonBytes, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("output: marshal json: %w", err)
	}

	return map[string][]byte{
		FileSubscription: []byte(sub),
		FileJSON:         jsonBytes,
		FileReadme:       buildReadme(brand, records),
	}, nil
}

// buildReadme renders a deterministic markdown table (no timestamps) so the
// artifact only changes when the data changes.
func buildReadme(brand string, records []publicRecord) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s working servers\n\n", brand)
	fmt.Fprintf(&b, "Total: %d servers\n\n", len(records))
	b.WriteString("| Country | Sequence | Speed |\n")
	b.WriteString("|---|---|---|\n")
	for _, r := range records {
		fmt.Fprintf(&b, "| %s | %s | %.1f MB/s |\n", r.Emoji, r.SeqName, r.SpeedMBps)
	}
	return []byte(b.String())
}

// renameLink sets the display name on a share link without touching its
// connection parameters: the JSON ps field for vmess, the URI fragment for the
// rest.
func renameLink(raw, name string) string {
	if strings.HasPrefix(raw, "vmess://") {
		if renamed, ok := renameVMess(raw, name); ok {
			return renamed
		}
	}
	base, _, _ := strings.Cut(raw, "#")
	return base + "#" + name
}

// renameVMess decodes the base64 JSON payload, updates ps, and re-encodes it.
func renameVMess(raw, name string) (string, bool) {
	payload := strings.TrimPrefix(raw, "vmess://")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	var obj map[string]any
	if err := json.Unmarshal(decoded, &obj); err != nil {
		return "", false
	}
	obj["ps"] = name
	reencoded, err := json.Marshal(obj)
	if err != nil {
		return "", false
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(reencoded), true
}

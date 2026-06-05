package output

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/whitedns/vless-tester/internal/model"
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

// unknownFlag is used when the country could not be determined (matches the
// WhiteDNS list, which tags unknown nodes with a question mark and an "OT" seq).
const unknownFlag = "❓"

// PublicServer is the public, leak-free view of an approved server. It carries
// only what users and the README need; no worker, vantage or diagnostic data.
type PublicServer struct {
	RawURI    string
	Country   string
	SeqName   string
	SpeedMBps float64
	// Tags are the public media-unlock tags shown in the node name (e.g.
	// "GPT⁺-FR", "NF-US"); derived from passed media checks via MediaTags.
	Tags []string
}

// Options tunes artifact generation.
type Options struct {
	Brand  string // defaults to DefaultBrand
	Prefix string // optional, prepended to every node name (node-prefix)
}

// NodeName renders the display name in the WhiteDNS format, e.g.
// "🇫🇷 | @WhiteDNS | FR1|12.3MB/s|GPT⁺-FR|GM-FR". The "{flag} | {brand} | "
// prefix keeps spaces; the seq, speed and media tags after it are joined by "|"
// with no spaces. An optional node-prefix is prepended verbatim.
func NodeName(brand, prefix string, s PublicServer) string {
	if brand == "" {
		brand = DefaultBrand
	}
	flag := naming.Emoji(s.Country)
	if flag == "" {
		flag = unknownFlag
	}
	fields := append([]string{s.SeqName, formatSpeed(s.SpeedMBps)}, s.Tags...)
	return prefix + flag + " | " + brand + " | " + strings.Join(fields, "|")
}

// formatSpeed renders throughput like WhiteDNS: KB/s below 1 MB/s, otherwise
// MB/s with one decimal, with no space before the unit.
func formatSpeed(mbps float64) string {
	if mbps < 1 {
		return fmt.Sprintf("%dKB/s", int(math.Round(mbps*1000)))
	}
	return fmt.Sprintf("%.1fMB/s", mbps)
}

// mediaTagOrder is the stable order media tags appear in the node name (matches
// WhiteDNS: GPT, GM, CL, SP, then the rest).
var mediaTagOrder = []string{"openai", "gemini", "claude", "spotify", "netflix", "youtube", "disney", "tiktok"}

// mediaTagAbbrev maps a media platform to its public node-name tag.
var mediaTagAbbrev = map[string]string{
	"openai":  "GPT⁺",
	"gemini":  "GM",
	"claude":  "CL",
	"spotify": "SP",
	"netflix": "NF",
	"youtube": "YT",
	"disney":  "DP",
	"tiktok":  "TT",
}

// MediaTags renders the passed media-unlock checks as WhiteDNS-style node-name
// tags ("GPT⁺-FR", "NF-US"), suffixed with the unlock region (the check detail
// when it is a 2-letter code, else the node country). Failed checks, ip_risk and
// unknown platforms are skipped; order follows mediaTagOrder.
func MediaTags(country string, checks []model.CheckOutcome) []string {
	byName := make(map[string]model.CheckOutcome, len(checks))
	for _, c := range checks {
		byName[c.Name] = c
	}
	var tags []string
	for _, name := range mediaTagOrder {
		c, ok := byName[name]
		if !ok || !c.Passed {
			continue
		}
		region := regionFromDetail(c.Detail)
		if region == "" {
			region = strings.ToUpper(country)
		}
		tag := mediaTagAbbrev[name]
		if region != "" {
			tag += "-" + region
		}
		tags = append(tags, tag)
	}
	return tags
}

// regionFromDetail returns an uppercase 2-letter region when the check detail is
// exactly a country code, else "".
func regionFromDetail(detail string) string {
	d := strings.ToUpper(strings.TrimSpace(detail))
	if len(d) == 2 && d[0] >= 'A' && d[0] <= 'Z' && d[1] >= 'A' && d[1] <= 'Z' {
		return d
	}
	return ""
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
		name := NodeName(brand, opts.Prefix, s)
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

// RenameLink sets the public display name on a share link without touching its
// connection parameters. It is exported for the multi-format converters
// (internal/convert), which reuse the same vmess-ps / fragment renaming as the
// base64 subscription so every format presents identical node names.
func RenameLink(raw, name string) string { return renameLink(raw, name) }

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

package checks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// Media unlock checks probe whether a streaming/AI service is usable from the
// vantage of the proxy under test. They run in the checks phase, after latency,
// through the same SOCKS client. Detection is heuristic by nature (providers
// change their gating often), so each probe keeps its classification in one
// small, unit-tested function that is easy to adjust.

// MediaResult is the outcome of one platform probe: Unlocked plus a short,
// human-readable detail (region, tier, or reason).
type MediaResult struct {
	Unlocked bool
	Detail   string
}

// probe fetches and classifies one platform. A probe may issue several requests.
type probe func(ctx context.Context, client *http.Client) MediaResult

// mediaProbes is the registry of supported platforms.
var mediaProbes = map[string]probe{
	"youtube": probeYouTube,
	"netflix": probeNetflix,
	"openai":  probeOpenAI,
	"gemini":  probeGemini,
	"claude":  probeClaude,
	"spotify": probeSpotify,
	"disney":  probeDisney,
	"tiktok":  probeTikTok,
}

// KnownMediaPlatforms returns the supported platform names (sorted-stable order
// is not guaranteed; callers that need order should sort).
func KnownMediaPlatforms() []string {
	names := make([]string, 0, len(mediaProbes))
	for name := range mediaProbes {
		names = append(names, name)
	}
	return names
}

// MediaCheck adapts a platform probe to the Check interface so it slots into the
// funnel like latency and speed.
type MediaCheck struct {
	Platform string
	Timeout  time.Duration
}

func (c MediaCheck) Name() string          { return "media:" + c.Platform }
func (c MediaCheck) Phase() model.JobPhase { return model.PhaseChecks }

func (c MediaCheck) Run(ctx context.Context, client *http.Client) (Result, error) {
	p, ok := mediaProbes[c.Platform]
	if !ok {
		return Result{}, fmt.Errorf("media: unknown platform %q", c.Platform)
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	mr := p(ctx, client)
	return Result{Passed: mr.Unlocked, Detail: mr.Detail}, nil
}

// NewMediaChecks builds checks for the named platforms, skipping unknown ones.
func NewMediaChecks(platforms []string, timeout time.Duration) []MediaCheck {
	out := make([]MediaCheck, 0, len(platforms))
	for _, name := range platforms {
		name = strings.ToLower(strings.TrimSpace(name))
		if _, ok := mediaProbes[name]; ok {
			out = append(out, MediaCheck{Platform: name, Timeout: timeout})
		}
	}
	return out
}

// --- fetch helper ---

// fetch issues a GET and returns the status and a capped body. A transport error
// yields status 0 and the error text as body, so probes classify uniformly.
func fetch(ctx context.Context, client *http.Client, url string) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err.Error()
	}
	// A realistic UA avoids trivial bot blocks that would mask real geo-gating.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	return resp.StatusCode, string(body)
}

// --- per-platform probes ---

// traceLoc extracts the loc= country from a Cloudflare cdn-cgi/trace body.
var traceLocRe = regexp.MustCompile(`(?m)^loc=([A-Z]{2})`)

func locFromTrace(body string) string {
	if m := traceLocRe.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

func probeYouTube(ctx context.Context, client *http.Client) MediaResult {
	status, body := fetch(ctx, client, "https://www.youtube.com/premium")
	return classifyYouTube(status, body)
}

func classifyYouTube(status int, body string) MediaResult {
	if status == 0 {
		return MediaResult{false, "unreachable"}
	}
	low := strings.ToLower(body)
	if strings.Contains(low, "premium is not available") || strings.Contains(low, "not available in your country") {
		return MediaResult{false, "blocked"}
	}
	region := matchGroup(`"countryCode"\s*:\s*"([A-Z]{2})"`, body)
	if strings.Contains(low, "youtube premium") || strings.Contains(low, "ad-free") {
		return MediaResult{true, regionOr(region, "available")}
	}
	return MediaResult{false, "blocked"}
}

func probeNetflix(ctx context.Context, client *http.Client) MediaResult {
	// 80018499 is a non-Original title: a 200 means full catalog access, a 404
	// means Originals-only, anything else means blocked.
	status, _ := fetch(ctx, client, "https://www.netflix.com/title/80018499")
	return classifyNetflix(status)
}

func classifyNetflix(status int) MediaResult {
	switch status {
	case 200:
		return MediaResult{true, "full"}
	case 404:
		return MediaResult{true, "originals only"}
	case 0:
		return MediaResult{false, "unreachable"}
	default:
		return MediaResult{false, "blocked"}
	}
}

func probeOpenAI(ctx context.Context, client *http.Client) MediaResult {
	cStatus, cBody := fetch(ctx, client, "https://api.openai.com/compliance/cookie_requirements")
	_, tBody := fetch(ctx, client, "https://chatgpt.com/cdn-cgi/trace")
	return classifyOpenAI(cStatus, cBody, locFromTrace(tBody))
}

func classifyOpenAI(status int, body, loc string) MediaResult {
	if status == 0 {
		return MediaResult{false, "unreachable"}
	}
	if strings.Contains(strings.ToLower(body), "unsupported_country") {
		return MediaResult{false, regionOr(loc, "blocked")}
	}
	return MediaResult{true, regionOr(loc, "available")}
}

func probeGemini(ctx context.Context, client *http.Client) MediaResult {
	status, body := fetch(ctx, client, "https://gemini.google.com/")
	return classifyGemini(status, body)
}

func classifyGemini(status int, body string) MediaResult {
	if status == 0 {
		return MediaResult{false, "unreachable"}
	}
	// The app embeds this marker only where Gemini is served.
	if strings.Contains(body, "45631641") {
		return MediaResult{true, "available"}
	}
	return MediaResult{false, "blocked"}
}

// claudeSupported is Anthropic's broad availability set; the probe reads the
// edge country and checks membership. Kept as a set so it is easy to update.
var claudeSupported = stringSet(
	"US", "GB", "CA", "AU", "NZ", "IE", "FR", "DE", "IT", "ES", "PT", "NL", "BE",
	"AT", "CH", "SE", "NO", "DK", "FI", "PL", "CZ", "RO", "GR", "JP", "KR", "SG",
	"MY", "TH", "ID", "PH", "VN", "IN", "IL", "ZA", "MX", "BR", "AR", "CL", "TW",
)

func probeClaude(ctx context.Context, client *http.Client) MediaResult {
	_, body := fetch(ctx, client, "https://claude.ai/cdn-cgi/trace")
	return classifyClaude(locFromTrace(body))
}

func classifyClaude(loc string) MediaResult {
	if loc == "" {
		return MediaResult{false, "unreachable"}
	}
	if claudeSupported[loc] {
		return MediaResult{true, loc}
	}
	return MediaResult{false, "blocked " + loc}
}

func probeSpotify(ctx context.Context, client *http.Client) MediaResult {
	// Account registration is the region-gated surface; a 200 means the country
	// is served. We only read the country marker without creating anything.
	status, body := fetch(ctx, client, "https://www.spotify.com/")
	return classifySpotify(status, body)
}

func classifySpotify(status int, body string) MediaResult {
	if status == 0 {
		return MediaResult{false, "unreachable"}
	}
	if status >= 400 {
		return MediaResult{false, "blocked"}
	}
	region := matchGroup(`https://www\.spotify\.com/([a-z]{2})-`, body)
	return MediaResult{true, regionOr(strings.ToUpper(region), "available")}
}

func probeDisney(ctx context.Context, client *http.Client) MediaResult {
	status, body := fetch(ctx, client, "https://www.disneyplus.com/")
	return classifyDisney(status, body)
}

func classifyDisney(status int, body string) MediaResult {
	if status == 0 {
		return MediaResult{false, "unreachable"}
	}
	low := strings.ToLower(body)
	if strings.Contains(low, "unavailable") || strings.Contains(low, "not available") {
		return MediaResult{false, "blocked"}
	}
	if status >= 400 {
		return MediaResult{false, "blocked"}
	}
	return MediaResult{true, "available"}
}

func probeTikTok(ctx context.Context, client *http.Client) MediaResult {
	status, body := fetch(ctx, client, "https://www.tiktok.com/")
	return classifyTikTok(status, body)
}

func classifyTikTok(status int, body string) MediaResult {
	if status == 0 {
		return MediaResult{false, "unreachable"}
	}
	region := matchGroup(`"region"\s*:\s*"([A-Z]{2})"`, body)
	if region == "" {
		region = matchGroup(`"countryCode"\s*:\s*"([A-Z]{2})"`, body)
	}
	if status >= 400 {
		return MediaResult{false, "blocked"}
	}
	return MediaResult{true, regionOr(region, "available")}
}

// --- small helpers ---

func matchGroup(pattern, s string) string {
	re := regexp.MustCompile(pattern)
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func regionOr(region, fallback string) string {
	if region != "" {
		return region
	}
	return fallback
}

func stringSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

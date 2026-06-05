package checks

import "testing"

// These exercise the pure classification logic with representative responses, so
// the suite needs no network and stays deterministic even as real endpoints drift.

func TestClassifyYouTube(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		unlocked bool
		detail   string
	}{
		{"available with region", 200, `{"countryCode":"US"} YouTube Premium ad-free`, true, "US"},
		{"available no region", 200, `Get YouTube Premium today`, true, "available"},
		{"blocked", 200, `YouTube Premium is not available in your country`, false, "blocked"},
		{"unreachable", 0, `dial tcp: timeout`, false, "unreachable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyYouTube(c.status, c.body)
			if got.Unlocked != c.unlocked || got.Detail != c.detail {
				t.Fatalf("got %+v, want {%v %q}", got, c.unlocked, c.detail)
			}
		})
	}
}

func TestClassifyNetflix(t *testing.T) {
	cases := []struct {
		status   int
		unlocked bool
		detail   string
	}{
		{200, true, "full"},
		{404, true, "originals only"},
		{403, false, "blocked"},
		{0, false, "unreachable"},
	}
	for _, c := range cases {
		got := classifyNetflix(c.status)
		if got.Unlocked != c.unlocked || got.Detail != c.detail {
			t.Fatalf("status %d: got %+v, want {%v %q}", c.status, got, c.unlocked, c.detail)
		}
	}
}

func TestClassifyOpenAI(t *testing.T) {
	if got := classifyOpenAI(200, `{"ok":true}`, "US"); !got.Unlocked || got.Detail != "US" {
		t.Fatalf("available: got %+v", got)
	}
	if got := classifyOpenAI(403, `{"messages":"unsupported_country"}`, "CN"); got.Unlocked || got.Detail != "CN" {
		t.Fatalf("blocked: got %+v", got)
	}
	if got := classifyOpenAI(0, "timeout", ""); got.Unlocked || got.Detail != "unreachable" {
		t.Fatalf("unreachable: got %+v", got)
	}
}

func TestClassifyGemini(t *testing.T) {
	if got := classifyGemini(200, `data...45631641,null,true...`); !got.Unlocked {
		t.Fatalf("available: got %+v", got)
	}
	if got := classifyGemini(200, `sorry, not here`); got.Unlocked {
		t.Fatalf("blocked: got %+v", got)
	}
}

func TestClassifyClaude(t *testing.T) {
	if got := classifyClaude("US"); !got.Unlocked || got.Detail != "US" {
		t.Fatalf("supported: got %+v", got)
	}
	if got := classifyClaude("CN"); got.Unlocked {
		t.Fatalf("unsupported: got %+v", got)
	}
	if got := classifyClaude(""); got.Unlocked || got.Detail != "unreachable" {
		t.Fatalf("no loc: got %+v", got)
	}
}

func TestClassifySpotify(t *testing.T) {
	if got := classifySpotify(200, `href="https://www.spotify.com/de-de/"`); !got.Unlocked || got.Detail != "DE" {
		t.Fatalf("region: got %+v", got)
	}
	if got := classifySpotify(403, ``); got.Unlocked {
		t.Fatalf("blocked: got %+v", got)
	}
}

func TestClassifyTikTok(t *testing.T) {
	if got := classifyTikTok(200, `{"region":"JP"}`); !got.Unlocked || got.Detail != "JP" {
		t.Fatalf("region: got %+v", got)
	}
	if got := classifyTikTok(0, ``); got.Unlocked {
		t.Fatalf("unreachable: got %+v", got)
	}
}

func TestLocFromTrace(t *testing.T) {
	body := "fl=123\nh=chatgpt.com\nloc=DE\ntls=TLSv1.3\n"
	if loc := locFromTrace(body); loc != "DE" {
		t.Fatalf("loc = %q, want DE", loc)
	}
	if loc := locFromTrace("no loc here"); loc != "" {
		t.Fatalf("expected empty loc, got %q", loc)
	}
}

func TestNewMediaChecksFiltersUnknown(t *testing.T) {
	got := NewMediaChecks([]string{"openai", " Netflix ", "bogus", "spotify"}, 0)
	if len(got) != 3 {
		t.Fatalf("want 3 known checks, got %d (%+v)", len(got), got)
	}
	if got[0].Name() != "media:openai" || got[1].Platform != "netflix" {
		t.Fatalf("unexpected normalization: %+v", got)
	}
}

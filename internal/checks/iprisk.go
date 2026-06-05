package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// IP-risk scoring estimates how "flagged" the proxy's exit IP looks, so the
// admin can spot nodes likely to be blocked by anti-fraud systems (the same idea
// as subs-check's IP-risk). The probe runs through the proxy under test, so the
// queried IP is the exit IP, and asks a reputation provider whether that IP is a
// known proxy/VPN, a datacenter/hosting IP, or mobile. Scoring lives in one
// small, unit-tested function so the weights are easy to tune.

// DefaultIPRiskURL is the free ip-api.com endpoint. Queried with no IP it
// reports the caller's IP, which through the proxy is the exit IP. The numeric
// `fields` selects status, query, countryCode, as, and the proxy/hosting/mobile
// security flags.
const DefaultIPRiskURL = "http://ip-api.com/json/?fields=status,message,query,countryCode,as,proxy,hosting,mobile"

// Risk score weights (0-100 scale). A node hosted on a datacenter IP is common
// and only moderately risky; an IP flagged as an anonymizing proxy/VPN is the
// strong signal, while a mobile IP looks residential and lowers the score.
const (
	riskProxyWeight   = 70
	riskHostingWeight = 25
	riskMobileBonus   = 20 // subtracted
	// RiskThreshold is the score at or above which a node is considered risky.
	RiskThreshold = 50
)

// ipRiskResponse is the subset of the provider payload we read.
type ipRiskResponse struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	Query       string `json:"query"`
	CountryCode string `json:"countryCode"`
	AS          string `json:"as"`
	Proxy       bool   `json:"proxy"`
	Hosting     bool   `json:"hosting"`
	Mobile      bool   `json:"mobile"`
}

// RiskResult is the outcome of an IP-risk probe: a 0-100 score, a low/high
// classification against RiskThreshold, and a short human-readable detail. OK is
// false when the lookup did not return usable data (unreachable, unreadable, or
// a provider error), so callers can avoid recording a misleading "clean" score.
type RiskResult struct {
	OK      bool
	Score   float64
	Risky   bool
	Detail  string
	Country string
}

// IPRiskCheck probes the exit IP's reputation. URL defaults to DefaultIPRiskURL.
type IPRiskCheck struct {
	URL     string
	Timeout time.Duration
}

func (c IPRiskCheck) Name() string { return "ip_risk" }

// Run fetches the provider payload through the proxy and scores it. A transport
// or provider error yields an "unknown" result (score 0, not risky) so a failed
// probe never penalizes a node.
func (c IPRiskCheck) Run(ctx context.Context, client *http.Client) (RiskResult, error) {
	url := c.URL
	if url == "" {
		url = DefaultIPRiskURL
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	status, body := fetch(ctx, client, url)
	if status == 0 {
		return RiskResult{Detail: "unreachable"}, nil
	}
	var resp ipRiskResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return RiskResult{Detail: "unreadable"}, nil
	}
	return scoreIPRisk(resp), nil
}

// scoreIPRisk maps a provider response to a risk score and classification. It is
// pure and exhaustively unit-tested; the network probe just feeds it.
func scoreIPRisk(r ipRiskResponse) RiskResult {
	if r.Status != "success" {
		detail := "lookup failed"
		if r.Message != "" {
			detail = r.Message
		}
		return RiskResult{Detail: detail}
	}

	score := 0
	var reasons []string
	if r.Proxy {
		score += riskProxyWeight
		reasons = append(reasons, "proxy")
	}
	if r.Hosting {
		score += riskHostingWeight
		reasons = append(reasons, "hosting")
	}
	if r.Mobile {
		score -= riskMobileBonus
		reasons = append(reasons, "mobile")
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	detail := "clean"
	if len(reasons) > 0 {
		detail = strings.Join(reasons, ",")
	}
	if r.CountryCode != "" {
		detail = fmt.Sprintf("%s (%s)", detail, r.CountryCode)
	}

	return RiskResult{
		OK:      true,
		Score:   float64(score),
		Risky:   score >= RiskThreshold,
		Detail:  detail,
		Country: r.CountryCode,
	}
}

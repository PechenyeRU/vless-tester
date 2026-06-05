package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScoreIPRisk(t *testing.T) {
	tests := []struct {
		name      string
		resp      ipRiskResponse
		wantOK    bool
		wantScore float64
		wantRisky bool
	}{
		{
			name:      "clean residential",
			resp:      ipRiskResponse{Status: "success", CountryCode: "DE"},
			wantOK:    true,
			wantScore: 0,
			wantRisky: false,
		},
		{
			name:      "datacenter only is moderate",
			resp:      ipRiskResponse{Status: "success", Hosting: true, CountryCode: "US", AS: "AS15169 Google"},
			wantOK:    true,
			wantScore: riskHostingWeight,
			wantRisky: false,
		},
		{
			name:      "flagged proxy is risky",
			resp:      ipRiskResponse{Status: "success", Proxy: true, CountryCode: "NL"},
			wantOK:    true,
			wantScore: riskProxyWeight,
			wantRisky: true,
		},
		{
			name:      "proxy on datacenter caps high",
			resp:      ipRiskResponse{Status: "success", Proxy: true, Hosting: true},
			wantOK:    true,
			wantScore: riskProxyWeight + riskHostingWeight,
			wantRisky: true,
		},
		{
			name:      "mobile lowers the score",
			resp:      ipRiskResponse{Status: "success", Hosting: true, Mobile: true},
			wantOK:    true,
			wantScore: riskHostingWeight - riskMobileBonus,
			wantRisky: false,
		},
		{
			name:      "mobile alone never goes negative",
			resp:      ipRiskResponse{Status: "success", Mobile: true},
			wantOK:    true,
			wantScore: 0,
			wantRisky: false,
		},
		{
			name:   "provider failure is not ok",
			resp:   ipRiskResponse{Status: "fail", Message: "private range"},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scoreIPRisk(tt.resp)
			if got.OK != tt.wantOK {
				t.Fatalf("OK = %v, want %v (detail %q)", got.OK, tt.wantOK, got.Detail)
			}
			if !tt.wantOK {
				return
			}
			if got.Score != tt.wantScore {
				t.Errorf("Score = %v, want %v", got.Score, tt.wantScore)
			}
			if got.Risky != tt.wantRisky {
				t.Errorf("Risky = %v, want %v", got.Risky, tt.wantRisky)
			}
		})
	}
}

func TestIPRiskCheckRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","query":"1.2.3.4","countryCode":"NL","proxy":true,"hosting":true,"mobile":false}`))
	}))
	defer srv.Close()

	got, err := IPRiskCheck{URL: srv.URL}.Run(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !got.OK || got.Score != riskProxyWeight+riskHostingWeight || !got.Risky {
		t.Fatalf("run result = %+v", got)
	}
}

func TestIPRiskCheckRunUnreachable(t *testing.T) {
	// A closed server makes the request fail; the probe must report not-OK.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	got, err := IPRiskCheck{URL: url}.Run(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.OK {
		t.Fatalf("expected not OK on unreachable, got %+v", got)
	}
}

func TestScoreIPRiskDetailCarriesReasonsAndCountry(t *testing.T) {
	got := scoreIPRisk(ipRiskResponse{Status: "success", Proxy: true, Hosting: true, CountryCode: "US"})
	if got.Detail != "proxy,hosting (US)" {
		t.Fatalf("detail = %q, want %q", got.Detail, "proxy,hosting (US)")
	}
	got = scoreIPRisk(ipRiskResponse{Status: "success", CountryCode: "JP"})
	if got.Detail != "clean (JP)" {
		t.Fatalf("clean detail = %q, want %q", got.Detail, "clean (JP)")
	}
}

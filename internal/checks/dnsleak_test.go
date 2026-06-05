package checks

import "testing"

func TestClassifyDNSLeak(t *testing.T) {
	tests := []struct {
		name        string
		dnsGeo      string
		exitCountry string
		wantOK      bool
		wantLeak    bool
	}{
		{"same country no leak", "United States - Google", "United States", true, false},
		{"different country leaks", "Germany - Deutsche Telekom", "United States", true, true},
		{"case-insensitive match", "united states - isp", "United States", true, false},
		{"missing dns geo not ok", "", "United States", false, false},
		{"missing exit not ok", "United States - Google", "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyDNSLeak(tt.dnsGeo, tt.exitCountry)
			if got.OK != tt.wantOK || got.Leak != tt.wantLeak {
				t.Fatalf("classifyDNSLeak(%q,%q) = %+v, want OK=%v Leak=%v",
					tt.dnsGeo, tt.exitCountry, got, tt.wantOK, tt.wantLeak)
			}
		})
	}
}

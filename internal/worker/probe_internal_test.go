package worker

import (
	"testing"

	"github.com/whitedns/vless-tester/internal/model"
)

func TestPassesRequire(t *testing.T) {
	checks := []model.CheckOutcome{
		{Name: "openai", Passed: true},
		{Name: "netflix", Passed: false},
		{Name: "spotify", Passed: true},
	}
	cases := []struct {
		name    string
		require []string
		want    bool
	}{
		{"no requirement passes", nil, true},
		{"single met", []string{"openai"}, true},
		{"all met", []string{"openai", "spotify"}, true},
		{"one unmet fails", []string{"openai", "netflix"}, false},
		{"unknown platform fails", []string{"disney"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := passesRequire(checks, c.require); got != c.want {
				t.Fatalf("passesRequire(%v) = %v, want %v", c.require, got, c.want)
			}
		})
	}
}

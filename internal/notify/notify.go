// Package notify sends end-of-cycle notifications. It wraps shoutrrr so a single
// list of service URLs (telegram://, discord://, slack://, generic:// webhook,
// …) drives many destinations, behind a small mockable interface.
package notify

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/containrrr/shoutrrr"
	"github.com/containrrr/shoutrrr/pkg/router"

	"github.com/whitedns/vless-tester/internal/naming"
)

// Notifier delivers a short message to the configured destinations. The real
// implementation is Shoutrrr; tests inject a mock.
type Notifier interface {
	Notify(ctx context.Context, message string) error
}

// Shoutrrr fans a message out to one or more shoutrrr service URLs.
type Shoutrrr struct {
	sender *router.ServiceRouter
}

// NewShoutrrr builds a notifier for the given service URLs. It returns (nil, nil)
// when no URLs are configured, so callers can treat "no notifier" uniformly.
func NewShoutrrr(urls []string) (*Shoutrrr, error) {
	cleaned := make([]string, 0, len(urls))
	for _, u := range urls {
		if u = strings.TrimSpace(u); u != "" {
			cleaned = append(cleaned, u)
		}
	}
	if len(cleaned) == 0 {
		return nil, nil
	}
	sender, err := shoutrrr.CreateSender(cleaned...)
	if err != nil {
		return nil, fmt.Errorf("notify: create sender: %w", err)
	}
	return &Shoutrrr{sender: sender}, nil
}

// Notify sends the message to every configured service, joining any per-service
// errors. ctx is accepted for interface symmetry; shoutrrr sends synchronously.
func (s *Shoutrrr) Notify(_ context.Context, message string) error {
	var errs []string
	for _, err := range s.sender.Send(message, nil) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("notify: %s", strings.Join(errs, "; "))
	}
	return nil
}

// countryCount pairs a country with its approved-server count, for sorting.
type countryCount struct {
	country string
	n       int
}

// CycleMessage renders a short, public-only end-of-cycle summary: the brand, the
// total approved count and a per-country breakdown (most first). It exposes no
// worker, vantage or diagnostic data.
func CycleMessage(brand string, approved int, byCountry map[string]int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✅ %s — %d working servers", brand, approved)
	if len(byCountry) == 0 {
		return b.String()
	}
	counts := make([]countryCount, 0, len(byCountry))
	for c, n := range byCountry {
		counts = append(counts, countryCount{c, n})
	}
	// Most servers first; ties broken by country code for stable output.
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].n != counts[j].n {
			return counts[i].n > counts[j].n
		}
		return counts[i].country < counts[j].country
	})

	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		flag := naming.Emoji(c.country)
		label := c.country
		if label == "" {
			label = "OT"
		}
		if flag != "" {
			parts = append(parts, fmt.Sprintf("%s %s %d", flag, label, c.n))
		} else {
			parts = append(parts, fmt.Sprintf("%s %d", label, c.n))
		}
	}
	b.WriteString("\n")
	b.WriteString(strings.Join(parts, " · "))
	return b.String()
}

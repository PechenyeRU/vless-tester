package worker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/whitedns/vless-tester/internal/ident"
	"github.com/whitedns/vless-tester/internal/model"
	"golang.org/x/net/proxy"
)

// idPattern is the worker-id contract shared with the coordinator schema.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// ResolveID returns a stable worker id: the provided value when it matches the
// ^[A-Za-z0-9-]+$ contract, otherwise a freshly generated mnemonic. This lets an
// operator pin an id via WORKER_ID while defaulting to an auto name.
func ResolveID(envID string) string {
	if idPattern.MatchString(envID) {
		return envID
	}
	return ident.Mnemonic()
}

// ProxyClient builds the HTTP client for the control channel. An empty proxyURL
// yields a direct client; a socks5:// URL routes worker->coordinator traffic
// through that SOCKS5 (DESIGN 3.2), independent of the local sing-box proxy used
// to test servers.
func ProxyClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if proxyURL == "" {
		return &http.Client{Timeout: timeout}, nil
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("worker: parse COORDINATOR_PROXY: %w", err)
	}
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return nil, fmt.Errorf("worker: unsupported proxy scheme %q (want socks5)", u.Scheme)
	}

	var auth *proxy.Auth
	if u.User != nil {
		pw, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pw}
	}
	dialer, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("worker: socks5 dialer: %w", err)
	}
	cd, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("worker: socks5 dialer is not a ContextDialer")
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: cd.DialContext},
	}, nil
}

// Default capacity values, used when neither an env override nor a measurement
// is available. Latency probes are tiny so concurrency is high; speed tests
// saturate the link so concurrency is small (DESIGN 4).
const (
	defaultLatencyConc = 200
	defaultSpeedConc   = 4
)

// CapacityConfig carries operator overrides; a zero field means "derive it".
type CapacityConfig struct {
	Latency int
	Speed   int
	BwMbps  float64
}

// Capacity resolves the worker's declared capacity. Overrides win; otherwise
// concurrency falls back to defaults and bandwidth is taken from the baseline
// self-test (measure) when provided. measure may be nil (then BwMbps stays 0).
func Capacity(ctx context.Context, cfg CapacityConfig, measure func(context.Context) (float64, error)) model.Capacity {
	c := model.Capacity{Latency: cfg.Latency, Speed: cfg.Speed, BwMbps: cfg.BwMbps}
	if c.Latency <= 0 {
		c.Latency = defaultLatencyConc
	}
	if c.Speed <= 0 {
		c.Speed = defaultSpeedConc
	}
	if c.BwMbps <= 0 && measure != nil {
		if bw, err := measure(ctx); err == nil && bw > 0 {
			c.BwMbps = bw
		}
	}
	return c
}

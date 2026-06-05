package worker

import (
	"context"
	"errors"
	"regexp"
	"testing"
)

func TestResolveID(t *testing.T) {
	if got := ResolveID("swift-otter-9"); got != "swift-otter-9" {
		t.Fatalf("valid id not kept: %q", got)
	}
	re := regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	for _, bad := range []string{"", "has space", "under_score", "slash/here"} {
		got := ResolveID(bad)
		if got == bad {
			t.Fatalf("invalid id %q should be replaced", bad)
		}
		if !re.MatchString(got) {
			t.Fatalf("generated id %q invalid", got)
		}
	}
}

func TestProxyClientDirect(t *testing.T) {
	c, err := ProxyClient("", 0)
	if err != nil {
		t.Fatalf("direct: %v", err)
	}
	if c == nil || c.Transport != nil {
		t.Fatal("empty proxy should yield a default (direct) client")
	}
}

func TestProxyClientSOCKS5(t *testing.T) {
	c, err := ProxyClient("socks5://user:pass@127.0.0.1:1080", 0)
	if err != nil {
		t.Fatalf("socks5: %v", err)
	}
	if c.Transport == nil {
		t.Fatal("socks5 proxy should install a custom transport")
	}
}

func TestProxyClientRejectsBadScheme(t *testing.T) {
	if _, err := ProxyClient("http://127.0.0.1:8080", 0); err == nil {
		t.Fatal("non-socks5 scheme should be rejected")
	}
}

func TestProxyClientRejectsBadURL(t *testing.T) {
	if _, err := ProxyClient("://nope", 0); err == nil {
		t.Fatal("malformed url should error")
	}
}

func TestCapacityOverrides(t *testing.T) {
	measured := false
	c := Capacity(context.Background(),
		CapacityConfig{Latency: 50, Speed: 3, BwMbps: 12.5},
		func(context.Context) (float64, error) { measured = true; return 999, nil },
	)
	if c.Latency != 50 || c.Speed != 3 || c.BwMbps != 12.5 {
		t.Fatalf("overrides not honored: %+v", c)
	}
	if measured {
		t.Fatal("measure must not run when BwMbps is overridden")
	}
}

func TestCapacityDefaultsAndMeasure(t *testing.T) {
	c := Capacity(context.Background(), CapacityConfig{},
		func(context.Context) (float64, error) { return 42.0, nil },
	)
	if c.Latency != defaultLatencyConc || c.Speed != defaultSpeedConc {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if c.BwMbps != 42.0 {
		t.Fatalf("measured bandwidth not used: %+v", c)
	}
}

func TestCapacityMeasureError(t *testing.T) {
	c := Capacity(context.Background(), CapacityConfig{},
		func(context.Context) (float64, error) { return 0, errors.New("no net") },
	)
	if c.BwMbps != 0 {
		t.Fatalf("failed measurement should leave BwMbps zero: %+v", c)
	}
}

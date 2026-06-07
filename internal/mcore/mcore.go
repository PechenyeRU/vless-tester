// Package mcore runs the proxy-under-test in-process via the mihomo (Clash.Meta)
// core. Each server is parsed into a mihomo outbound and exposed as an
// *http.Client whose transport dials through that outbound — no subprocess, no
// SOCKS inbound, no temp config, no readiness poll. This is the testing core;
// the sing-box mapper in internal/core is kept only for the published sing-box
// subscription format (internal/convert).
package mcore

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"

	"github.com/whitedns/vless-tester/internal/convert"
	"github.com/whitedns/vless-tester/internal/model"
)

// Instance is a parsed in-process proxy plus an http.Client that dials through
// it. The caller must Close it to release the proxy's connections.
type Instance struct {
	proxy  C.Proxy
	client *http.Client
}

// Client returns the http.Client that routes through the proxy under test.
func (i *Instance) Client() *http.Client { return i.client }

// Close releases the client's idle connections and the underlying proxy.
func (i *Instance) Close() error {
	if i.client != nil {
		i.client.CloseIdleConnections()
	}
	if i.proxy != nil {
		_ = i.proxy.Close()
	}
	return nil
}

// Start parses a server into a mihomo proxy and builds an http.Client that dials
// through it. Unlike the old subprocess core there is no startup to wait for, so
// ctx is honored only by the later proxied requests. handshakeTimeout bounds the
// TLS handshake on each proxied connection; zero uses 10s.
func Start(_ context.Context, srv model.Server, handshakeTimeout time.Duration) (*Instance, error) {
	mapping := convert.ClashProxy(srv, "probe")
	if mapping == nil {
		return nil, fmt.Errorf("mcore: protocol %q has no clash mapping", srv.Protocol)
	}
	proxy, err := adapter.ParseProxy(mapping)
	if err != nil {
		return nil, fmt.Errorf("mcore: parse proxy: %w", err)
	}
	if handshakeTimeout <= 0 {
		handshakeTimeout = 10 * time.Second
	}
	// Dial through the mihomo outbound. We pass Host (the destination domain),
	// not a pre-resolved IP, so DNS resolution happens at the exit through the
	// tunnel rather than locally.
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			var dstPort uint16
			if n, err := strconv.ParseUint(port, 10, 16); err == nil {
				dstPort = uint16(n)
			}
			return proxy.DialContext(ctx, &C.Metadata{Host: host, DstPort: dstPort})
		},
		// Each measurement is independent; do not reuse connections across them.
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: handshakeTimeout,
	}
	// No client.Timeout: the checks govern overall deadlines via their context.
	return &Instance{proxy: proxy, client: &http.Client{Transport: transport}}, nil
}

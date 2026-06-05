package engine

import (
	"fmt"
	"net/http"
	"time"

	"golang.org/x/net/proxy"
)

// SOCKS5Client builds an http.Client whose transport dials through the given
// SOCKS5 address (the local sing-box inbound). It is the production ClientFactory.
func SOCKS5Client(socksAddr string) (*http.Client, error) {
	dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("engine: socks5 dialer: %w", err)
	}
	transport := &http.Transport{
		// Disable connection reuse so each measurement is independent.
		DisableKeepAlives:   true,
		MaxIdleConns:        0,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	cd, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("engine: socks5 dialer is not a ContextDialer")
	}
	transport.DialContext = cd.DialContext
	return &http.Client{Transport: transport}, nil
}

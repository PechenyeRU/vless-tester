// Package core maps a normalized Server to a sing-box outbound. It is no longer
// a runtime proxy engine (the proxy-under-test now runs in-process via mihomo;
// see internal/mcore); the mapper is kept only to render the sing-box
// subscription output format (internal/convert).
package core

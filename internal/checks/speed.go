package checks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// Speed test defaults. These mirror the settings seeds and are overridden by
// SpeedConfig fields when non-zero.
const (
	DefaultStreams      = 6
	DefaultBytes        = 10_000_000 // 10 MB target
	DefaultProbeBytes   = 1_000_000  // 1 MB adaptive probe
	DefaultProbeMinMBps = 0.5        // below this the full transfer is skipped
)

// SpeedConfig parameterizes a SpeedCheck. Zero-value fields fall back to the
// Default* constants, so the check is usable with an empty config in tests.
type SpeedConfig struct {
	DownloadURL string // a __down-style endpoint; the bytes count is appended
	UploadURL   string // a __up-style endpoint; empty disables the upload leg
	Streams     int
	Bytes       int
	Adaptive    bool
	ProbeBytes  int
	ProbeMinMBs float64
	Timeout     time.Duration
}

// SpeedCheck measures download and upload throughput using several parallel
// streams (a single stream rarely saturates a proxied link). When Adaptive is
// set it first probes a small transfer and only runs the full transfer if the
// probe looks promising, saving bandwidth on slow servers.
type SpeedCheck struct {
	Config SpeedConfig
}

func (c SpeedCheck) Name() string          { return "speed" }
func (c SpeedCheck) Phase() model.JobPhase { return model.PhaseSpeed }

func (c SpeedCheck) Run(ctx context.Context, client *http.Client) (Result, error) {
	timeout := c.Config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dl, err := c.measureDirection(ctx, client, directionDownload)
	if err != nil {
		return Result{Passed: false, Detail: err.Error()}, nil
	}
	res := Result{Passed: true, DlMbps: ptrFloat(dl)}

	if c.Config.UploadURL != "" {
		ul, err := c.measureDirection(ctx, client, directionUpload)
		if err != nil {
			return Result{Passed: false, Detail: err.Error()}, nil
		}
		res.UlMbps = ptrFloat(ul)
	}
	return res, nil
}

type direction int

const (
	directionDownload direction = iota
	directionUpload
)

// measureDirection runs the adaptive probe (if enabled) then the full transfer,
// returning aggregate throughput in MB/s.
func (c SpeedCheck) measureDirection(ctx context.Context, client *http.Client, dir direction) (float64, error) {
	if c.Config.Adaptive {
		probeMBs, err := c.transfer(ctx, client, dir, c.probeBytes())
		if err != nil {
			return 0, err
		}
		if probeMBs < c.probeMin() {
			// Too slow to be worth the full transfer; report the probe figure.
			return probeMBs, nil
		}
	}
	return c.transfer(ctx, client, dir, c.totalBytes())
}

// transfer drives `total` bytes across `streams` parallel connections and
// returns the aggregate throughput in MB/s.
func (c SpeedCheck) transfer(ctx context.Context, client *http.Client, dir direction, total int) (float64, error) {
	streams := c.streams()
	per := total / streams
	if per == 0 {
		per, streams = total, 1
	}

	var (
		moved   atomic.Int64
		firstMu sync.Mutex
		firstEr error
		wg      sync.WaitGroup
	)
	setErr := func(err error) {
		firstMu.Lock()
		if firstEr == nil {
			firstEr = err
		}
		firstMu.Unlock()
	}

	start := time.Now()
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := c.oneStream(ctx, client, dir, per)
			moved.Add(n)
			if err != nil {
				setErr(err)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()

	if firstEr != nil {
		return 0, firstEr
	}
	if elapsed <= 0 {
		elapsed = 1e-6
	}
	return float64(moved.Load()) / 1e6 / elapsed, nil
}

// oneStream transfers `n` bytes in one direction and returns the bytes moved.
func (c SpeedCheck) oneStream(ctx context.Context, client *http.Client, dir direction, n int) (int64, error) {
	switch dir {
	case directionDownload:
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, withBytes(c.Config.DownloadURL, n), nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return 0, fmt.Errorf("speed: download status %d", resp.StatusCode)
		}
		moved, err := io.Copy(io.Discard, resp.Body)
		return moved, err
	case directionUpload:
		body := io.LimitReader(zeroReader{}, int64(n))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, withBytes(c.Config.UploadURL, n), body)
		if err != nil {
			return 0, err
		}
		req.ContentLength = int64(n)
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 400 {
			return 0, fmt.Errorf("speed: upload status %d", resp.StatusCode)
		}
		return int64(n), nil
	default:
		return 0, fmt.Errorf("speed: unknown direction")
	}
}

func (c SpeedCheck) streams() int {
	if c.Config.Streams > 0 {
		return c.Config.Streams
	}
	return DefaultStreams
}

func (c SpeedCheck) totalBytes() int {
	if c.Config.Bytes > 0 {
		return c.Config.Bytes
	}
	return DefaultBytes
}

func (c SpeedCheck) probeBytes() int {
	if c.Config.ProbeBytes > 0 {
		return c.Config.ProbeBytes
	}
	return DefaultProbeBytes
}

func (c SpeedCheck) probeMin() float64 {
	if c.Config.ProbeMinMBs > 0 {
		return c.Config.ProbeMinMBs
	}
	return DefaultProbeMinMBps
}

// withBytes appends or overrides the bytes query parameter on a transfer URL.
func withBytes(raw string, n int) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set("bytes", strconv.Itoa(n))
	u.RawQuery = q.Encode()
	return u.String()
}

// zeroReader is an infinite source of zero bytes for upload payloads.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

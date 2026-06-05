// Package worker is the probe side of the fleet: a pull-based, untrusted client
// that claims jobs from the coordinator, runs the test battery through a local
// sing-box, and reports raw measurements. It owns no approval, naming, or
// publishing logic (DESIGN 3.2); all of that lives in the coordinator.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/whitedns/vless-tester/internal/model"
)

// Job mirrors the coordinator's claim response. Field tags must match
// internal/api; the client/handler round-trip tests guard against drift.
type Job struct {
	JobID    int64  `json:"job_id"`
	ServerID int64  `json:"server_id"`
	RawURI   string `json:"raw_uri"`
	Phase    string `json:"phase"`
	Protocol string `json:"protocol"`
	// Checks lists the media-unlock platforms the coordinator wants probed for
	// this job (empty when media checks are disabled).
	Checks []string `json:"checks,omitempty"`
	// Require lists platforms the server must unlock to be worth a speed test;
	// when set and unmet, the worker skips the expensive speed leg.
	Require []string `json:"require,omitempty"`
	// IPRisk asks the worker to score the exit IP's reputation (informational).
	IPRisk bool `json:"ip_risk,omitempty"`
	// IPRiskURL overrides the IP-risk provider URL (empty = worker default).
	IPRiskURL string `json:"ip_risk_url,omitempty"`
	// Stages is the ordered, gateable funnel pipeline (media, ip_risk, speed) the
	// coordinator wants run after latency; empty means use the built-in default.
	Stages []model.FunnelStage `json:"stages,omitempty"`
	// Speed overrides the worker's speed-test config (custom endpoints, sizing,
	// timeout); nil keeps the worker default.
	Speed *model.SpeedSpec `json:"speed,omitempty"`
}

// Result is one measurement the worker reports back for a claimed job.
type Result struct {
	JobID     int64                `json:"job_id"`
	Status    string               `json:"status"`
	LatencyMs *int                 `json:"latency_ms,omitempty"`
	DlMbps    *float64             `json:"dl_mbps,omitempty"`
	UlMbps    *float64             `json:"ul_mbps,omitempty"`
	Error     string               `json:"error,omitempty"`
	Checks    []model.CheckOutcome `json:"checks,omitempty"`
}

// Client is the HTTP control-plane client. HTTP may be a plain client or one
// whose transport tunnels through COORDINATOR_PROXY (see ProxyClient).
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Register announces the worker and returns its id. A blank id asks the
// coordinator to assign a mnemonic; the returned id is authoritative.
func (c *Client) Register(ctx context.Context, id string, capacity model.Capacity) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.post(ctx, "/api/v1/workers/register", map[string]any{
		"id": id, "capacity": capacity,
	}, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// Heartbeat refreshes the worker's liveness and free capacity.
func (c *Client) Heartbeat(ctx context.Context, id, status string, free model.Capacity) error {
	return c.post(ctx, "/api/v1/workers/heartbeat", map[string]any{
		"id": id, "status": status, "capacity_free": free,
	}, nil)
}

// Claim leases up to max queued jobs for the given phase ("" = any).
func (c *Client) Claim(ctx context.Context, workerID string, phase model.JobPhase, max int) ([]Job, error) {
	var jobs []Job
	if err := c.post(ctx, "/api/v1/jobs/claim", map[string]any{
		"worker_id": workerID, "phase": string(phase), "max": max,
	}, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// Report submits measurements; it returns how many the coordinator accepted.
func (c *Client) Report(ctx context.Context, workerID string, results []Result) (int, error) {
	var resp struct {
		Accepted int `json:"accepted"`
	}
	if err := c.post(ctx, "/api/v1/jobs/results", map[string]any{
		"worker_id": workerID, "results": results,
	}, &resp); err != nil {
		return 0, err
	}
	return resp.Accepted, nil
}

// Nack releases claimed jobs the worker will not complete.
func (c *Client) Nack(ctx context.Context, workerID string, jobIDs []int64) error {
	return c.post(ctx, "/api/v1/jobs/nack", map[string]any{
		"worker_id": workerID, "job_ids": jobIDs,
	}, nil)
}

// post sends a JSON body and, when out is non-nil, decodes the JSON response.
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("worker: marshal %s: %w", path, err)
	}
	url := strings.TrimRight(c.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("worker: request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("worker: post %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("worker: %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("worker: decode %s: %w", path, err)
		}
	}
	return nil
}

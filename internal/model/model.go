// Package model holds the core domain entities shared across ingest, store,
// checks and output. Keeping them in one dependency-free package avoids import
// cycles between the pipeline stages.
package model

import "time"

// Protocol identifies a proxy protocol supported by the tester.
type Protocol string

const (
	ProtocolVLESS       Protocol = "vless"
	ProtocolVMess       Protocol = "vmess"
	ProtocolHysteria2   Protocol = "hysteria2"
	ProtocolHysteria    Protocol = "hysteria" // v1
	ProtocolTUIC        Protocol = "tuic"
	ProtocolTrojan      Protocol = "trojan"
	ProtocolShadowsocks Protocol = "ss"
	ProtocolAnyTLS      Protocol = "anytls"
	ProtocolSOCKS       Protocol = "socks"
)

// Server is a unique, deduplicated proxy endpoint. Params carries the
// protocol-specific fields (uuid, password, transport, tls...) that the core
// package needs to render a sing-box outbound; it is not persisted column-wise.
type Server struct {
	ID          int64
	Fingerprint string
	RawURI      string
	Protocol    Protocol
	Host        string
	Port        int
	Country     string // ISO-3166 alpha-2, empty until GeoIP lookup
	SeqName     string // stable per-country name, e.g. "FR110"
	Params      map[string]string
	// Credential is the protocol secret (uuid, password, or "uuid:password").
	// It is parsed from the link and used by the core to build an outbound; it
	// is never persisted as a column (raw_uri already carries it).
	Credential string
	FirstSeen  time.Time
	LastSeen   time.Time
}

// JobPhase is a stage of the funnel pipeline.
type JobPhase string

const (
	PhaseLatency JobPhase = "latency"
	PhaseSpeed   JobPhase = "speed"
	PhaseChecks  JobPhase = "checks"
)

// JobState is the lifecycle state of a queued job.
type JobState string

const (
	JobQueued  JobState = "queued"
	JobClaimed JobState = "claimed"
	JobDone    JobState = "done"
	JobFailed  JobState = "failed"
)

// Job is a unit of work in the queue: test one server at one phase.
type Job struct {
	ID        int64
	ServerID  int64
	Phase     JobPhase
	State     JobState
	ClaimedBy string
	ClaimedAt time.Time
	Attempts  int
	CreatedAt time.Time
}

// Worker is a probe in the fleet. The mnemonic ID is its identity and vantage
// point; Capacity is auto-measured via the baseline self-test.
type Worker struct {
	ID       string
	Capacity Capacity
	Status   string
	LastSeen time.Time
}

// Capacity describes how much work a worker can take.
type Capacity struct {
	Latency int     `json:"latency"` // max concurrent latency probes
	Speed   int     `json:"speed"`   // max concurrent speed tests (servers)
	BwMbps  float64 `json:"bw_mbps"` // measured bandwidth budget
}

// RunStatus is the outcome of a test run.
type RunStatus string

const (
	StatusOK      RunStatus = "ok"
	StatusTimeout RunStatus = "timeout"
	StatusError   RunStatus = "error"
	StatusRefused RunStatus = "refused"
)

// TestRun is one measurement of a server from one worker. BatchID ties it to the
// coordinator cycle that produced it.
type TestRun struct {
	ID        int64
	ServerID  int64
	WorkerID  string
	BatchID   *int64
	Phase     JobPhase
	RunAt     time.Time
	LatencyMs *int
	DlMbps    *float64
	UlMbps    *float64
	Status    RunStatus
	Error     string
}

// Check is an extensible approval check result (reachability, geo, dns-leak).
type Check struct {
	ID       int64
	RunID    *int64
	ServerID int64
	Name     string
	Passed   bool
	Metric   *float64
	Detail   string
}

// SourceKind distinguishes raw link files from subscription URLs.
type SourceKind string

const (
	SourceRawFile         SourceKind = "raw_file"
	SourceSubscriptionURL SourceKind = "subscription_url"
)

// Source is an ingest origin.
type Source struct {
	ID        int64
	Kind      SourceKind
	Location  string
	LastFetch *time.Time
	Enabled   bool
}

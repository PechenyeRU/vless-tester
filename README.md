# vless-tester

A distributed tester and subscription builder for proxy nodes. It ingests proxy
configs from many sources, tests each node from multiple vantage points through a
funnel pipeline (latency → speed → media/geo/DNS checks), and publishes a curated
subscription of the working nodes — renamed, geo-tagged, and refreshed on a
schedule.

The core proxy engine is [sing-box](https://github.com/SagerNet/sing-box), driven
as a child process. Storage and the job queue live in PostgreSQL — no extra broker.

## Why

Public proxy subscriptions are noisy: full of dead, slow, or geo-blocked nodes.
vless-tester continuously measures them from real vantage points and republishes
only the ones that actually work, named like a VPN provider:

```
🇫🇷 | @WhiteDNS | FR110 | 12.3 MB/s
```

## Architecture

```
   sources ──ingest──▶  COORDINATOR (control plane)   ◀── Web UI (SPA) + REST API
  (raw + sub URL)       • PostgreSQL: servers, runs, checks, job queue
                        • scheduler: refresh, retest, publish, geoip
                        • naming (geoip → emoji → stable seq name)
                        • output builder + publish to a separate Git repo
                                  ▲
                                  │  REST, pull-based (worker → coordinator)
                ┌─────────────────┼─────────────────┐
          ┌─────┴─────┐     ┌─────┴─────┐     ┌──────┴────┐
          │ worker    │     │ worker    │     │ worker    │   (replicas: N)
          │ (home)    │     │ (VPS)     │     │ (k8s)     │
          │ sing-box  │     │ sing-box  │     │ sing-box  │
          └───────────┘     └───────────┘     └───────────┘
```

- **Coordinator** — the only control plane and scheduler. It ingests sources,
  runs the job queue, assigns work, names nodes via GeoIP, applies the approval
  policy, and publishes the working list. It serves the REST API and embeds the
  admin dashboard (SvelteKit SPA) into the binary.
- **Worker** — a dumb, untrusted probe. It claims jobs, spins up sing-box, runs
  the checks, and reports **raw** measurements. It holds no policy, no thresholds,
  and no secrets, so a worker can be handed to an external contributor safely.
  It is pull-based, so it works behind NAT with no inbound ports.
- **Tester** — a single-process CLI that runs the whole pipeline locally against a
  file of share links (ingest → test → output). Useful for development and
  one-off runs without a fleet.

### Why pull-based

Workers open the connection to the coordinator and ask for work. This means they
run behind NAT, give natural backpressure (a worker asks only when it has free
capacity), and scaling out is just starting another worker that self-registers.

### Why Postgres as the queue

`SELECT ... FOR UPDATE SKIP LOCKED` handles tens of thousands of jobs without
extra infrastructure, and `LISTEN/NOTIFY` pushes live updates to the dashboard.

## Test pipeline

A fail-fast funnel — a node only advances to the next, more expensive stage if it
passed the previous one:

1. **Latency** — high concurrency; a `generate_204` probe through the proxy drops
   the dead nodes.
2. **Speed** — low concurrency per node, but each measurement opens multiple
   parallel streams and sums the throughput (a single stream rarely saturates a
   proxied link). Adaptive by default: a small probe escalates to the full
   download only if promising. Endpoints, stream count, and sizing are
   configurable.
3. **Checks** — extensible, pluggable probes: media/streaming unlock, IP risk
   scoring, DNS leak, and anything else implementing the `Check` interface.

Approval is computed **only** on the coordinator, from validated results in an
append-only history. Because history is append-only, changing the quality/quantity
gate and re-publishing does **not** require re-testing — the output builder simply
re-runs over the stored runs.

## Output

The working list is published to a **separate, dedicated Git repository** (URL
configurable from the dashboard). Public artifacts never expose internals — no
worker IDs, no per-vantage latencies, no source names. Multiple formats are built
from the same data:

- base64 subscription (importable in any client)
- Clash / Mihomo (ACL4SSR template)
- sing-box
- Surge
- v2ray

The subscription can be served directly by the coordinator at an optional
obfuscated `/sub/<token>` path.

## Quick start (local stack)

Requires Docker with the Compose plugin.

```bash
cp .env.example .env          # then edit ADMIN_PASSWORD, MaxMind creds, etc.
make stack                    # postgres + coordinator + worker, built from source
```

The dashboard is then at <http://localhost:8080>. Sign in with `ADMIN_USER` /
`ADMIN_PASSWORD`. Add sources, tune thresholds, and trigger a test cycle from the
admin panel — everything the system does is driveable from the UI.

To stop: `make stack-down`.

## Running a worker

A worker needs only the coordinator URL and a per-worker token (mint one in the
dashboard, *Workers* section). It embeds sing-box, so it is a single artifact with
no separate install.

**Container image (from GHCR):**

```bash
docker run --rm \
  -e COORDINATOR_URL=https://coordinator.example.com \
  -e WORKER_TOKEN=wt_... \
  ghcr.io/pechenyeru/vless-tester/worker:latest
```

**Single-file binary** (attached to each [release](https://github.com/PechenyeRU/vless-tester/releases)):

```bash
curl -fsSL -o worker https://github.com/PechenyeRU/vless-tester/releases/latest/download/worker-linux-amd64
chmod +x worker
COORDINATOR_URL=https://coordinator.example.com WORKER_TOKEN=wt_... ./worker
```

On restricted networks the control channel can be tunneled through a SOCKS5 proxy
with `COORDINATOR_PROXY=socks5://user:pass@host:1080`; this affects only the
worker→coordinator channel, never the proxied test traffic.

## Container images

Multi-arch (`linux/amd64`, `linux/arm64`) images are published to GHCR on every
tagged release:

| Image | Contents |
|-------|----------|
| `ghcr.io/pechenyeru/vless-tester/coordinator` | control plane + embedded dashboard, on distroless |
| `ghcr.io/pechenyeru/vless-tester/worker`      | probe + embedded sing-box, on distroless |

## Configuration

All configuration is via environment variables (12-factor); see
[`.env.example`](.env.example) for the full list. The essentials:

| Variable | Component | Purpose |
|----------|-----------|---------|
| `DATABASE_URL` | coordinator | PostgreSQL DSN |
| `ADMIN_USER` / `ADMIN_PASSWORD` | coordinator | dashboard login (admin plane is unauthenticated if the password is empty — dev only) |
| `MAXMIND_ACCOUNT_ID` / `MAXMIND_LICENSE_KEY` | coordinator | GeoLite2 download for geo naming |
| `GITHUB_PUBLISH_REPO` / `GITHUB_TOKEN` | coordinator | target repo for the published working list |
| `COORDINATOR_URL` | worker | control-plane endpoint |
| `WORKER_TOKEN` | worker | per-worker auth + identity (required) |
| `COORDINATOR_PROXY` | worker | optional SOCKS5 for the control channel only |
| `WORKER_CAP_SPEED` | worker | concurrent speed tests (default 4); raise on fast links — each test saturates the proxied node, not your uplink |
| `WORKER_CAP_LATENCY` | worker | concurrent latency probes + default claim batch size (default 200) |
| `WORKER_BW_MBPS` | worker | bandwidth reported to the coordinator for fleet sizing (not throttled locally); auto-measured when empty |

Runtime preferences (scheduler intervals, thresholds, approval policy, speed-test
parameters, output filters) live in the `settings` table and are editable from the
dashboard — no restart needed.

## Building from source

Requires Go (see [`go.mod`](go.mod)), Node 22 (for the dashboard), and `bash`/`curl`
(for fetching sing-box).

```bash
make build              # compile everything
make test               # unit tests
make test-int           # integration tests (needs TEST_DATABASE_URL)
make vet lint           # go vet + golangci-lint

make coordinator        # builds the SPA, embeds it, outputs bin/coordinator
make worker-embedded    # single-file worker for the host platform (bin/worker)
make dist               # multi-arch single-file workers into dist/
make docker             # build both container images
```

A one-off local run without a fleet or database wiring:

```bash
go run ./cmd/tester path/to/links.txt
```

## Extending the checks

Adding a new approval probe — site reachability, geo consistency, a new
streaming service — is a matter of implementing the `Check` interface; the
pipeline picks it up with no refactor. Check results are stored per-worker and
visible only in the admin dashboard, never in the public artifacts.

```go
type Check interface {
    Name() string
    Phase() string
    Run(ctx context.Context, client *http.Client) (CheckResult, error)
}
```

## Security

Lightweight, internal-facing by design: bearer tokens for worker → coordinator,
a dedicated token/deploy key for publishing, and TLS terminated at the ingress.
The worker is treated as untrusted — the coordinator validates and bounds every
reported measurement, and can require corroboration from multiple distinct
workers before approving a node.

## Layout

```
cmd/coordinator   control plane: ingest, API, scheduler, output, publish
cmd/worker        probe: claim → test → report
cmd/tester        local single-process pipeline (dev / one-off)
internal/ingest   URI parsers, subscription fetch, dedup, fingerprint
internal/core     sing-box config generation and process lifecycle
internal/checks    latency, speed, media, ip-risk, dns-leak (the Check interface)
internal/store    PostgreSQL: migrations, queries, job queue
internal/naming   geoip, emoji, stable sequence names
internal/output   subscription formats, README, Git publish
internal/convert  Clash / sing-box / Surge / v2ray renderers
internal/api      REST handlers (worker control plane + admin)
internal/worker   worker loop: capacity, concurrency, speed semaphore
web/              SvelteKit admin dashboard (embedded into the coordinator)
deploy/docker     coordinator and worker Dockerfiles
```

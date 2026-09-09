# Implementation Notes and Decision Record

## 1. Key Assumptions

1. **The deployment target may be ephemeral.** No Kubernetes exists on this machine, so `deploy`
   rolls out with Docker Compose (`deploy/docker-compose.deploy.yml`) on the CI runner and
   locally. If a real staging cluster existed, only the compose file and the four `docker compose`
   lines in `deploy/deploy.sh` would change; the digest-pinning and smoke-test logic would not.
2. **In-memory state is acceptable.** `MemoryStore` loses data on restart, so a deploy is a data
   reset. Hence the smoke test creates and deletes its own task instead of asserting on existing
   data. A real datastore would need a migration gate.
3. **The existing tests define a contract I may not break.** Task IDs start at 0 and
   `handler_test.go` string-matches `task_api_tasks_total 2`. Both look like defects; both are
   load-bearing. I treated them as the specification. If they are not, see the trade-off in §4.

## 2. Delivery Path

PR → `verify` (gofmt, `go vet`, golangci-lint v2.13.2, `go test -race`, build) → `image`
(builds, **publishes nothing**, enforces the 15 MiB budget and asserts the container reports
`healthy` and non-root). A PR is untrusted input, so it receives no registry credentials.

Merge to `main` → `verify` → `publish` (GHCR, tagged `sha-<commit>`, labelled
`org.opencontainers.image.revision`) → `deploy` (`deploy/deploy.sh`).

Artifact identity is the **digest**. `deploy` consumes `needs.publish.outputs.digest`, never
`:latest`, because a tag can be moved to point at different content. `task_api_build_info`
exposes the same revision at runtime, so the dashboard shows which commit is serving traffic.

**Rollback unit: the image digest.** `deploy.sh` records the running image before rolling out and
restores it if verification fails. Nothing else about the environment changes.

Credentials are `GITHUB_TOKEN` only — minted per run, scoped `packages: write` on the publish job
alone. No PAT is stored.

**Validation boundary:** the environment is ephemeral, torn down with the runner — a real rollout
of a real published artifact, not a long-lived URL.

## 3. One Actual Validation or Investigation

**Question:** are the reported latency percentiles true?

**Expected:** an in-memory map should answer in tens of microseconds.

**Observed:** `histogram_quantile` reported **p50 2.5 ms, p95 4.75 ms, p99 4.95 ms**, while
`sum/count` gave a true mean of **0.176 ms** — a ~28× discrepancy.

**Isolating the cause.** Either the service is slow, the experiment is wrong, or the instrument is.
- Raw buckets for `GET /tasks/{id}` showed **all 55 observations in the first bucket** (every
  cumulative count identical at 55). `prometheus.DefBuckets` starts at 5 ms, so
  `histogram_quantile` could only interpolate inside `[0, 0.005]`.
- An independent client measurement (`curl -w '%{time_total}'`, 30 requests) gave p50 1.9 ms
  *including* process startup and TCP connect — an upper bound that still excluded 4.95 ms.

The service was fine; the instrument was wrong.

**Change:** re-bucketed from the measured distribution, starting at 25 µs (`metrics.go`).

**Re-validation caught a second problem.** The first re-run still reported p99 ≈ 4.96 ms. That was
methodological: `rate(...[5m])` spanned data recorded under *both* bucket schemas, so `sum by (le)`
was merging two incompatible histograms. On a clean TSDB (`docker compose down -v`) the same query
gave **p50 0.163 ms, p95 0.446 ms, p99 0.697 ms** against a mean of 0.173 ms — a plausible tail.

**Signal confirmed further:** `GET /metrics` is the slowest route at **3.17 ms**, 15–108× any
business route, and scraped every 5 s forever. **112 of its 185 series are latency buckets** — my
own re-bucketing grew that from 84. Resolution is not free; it is paid on every scrape. Hence the
dashboard excludes `/metrics` and `/healthz` from business panels.

**A second defect this surfaced:** the 5xx panel rendered *empty*, not zero, because
`sum(rate(...))` over a non-existent series returns an empty vector. An on-call engineer could not
distinguish "no errors" from "broken metric". Fixed with `or vector(0)`.

## 4. Two Engineering Trade-offs

**(a) Kept a metric name that violates Prometheus convention.** `task_api_tasks_total` is a gauge
whose name ends in `_total`, a suffix reserved for counters. Renaming it would break
`handler_test.go` and any existing dashboard or alert. I kept the legacy names and added a
correctly-named `task_api_tasks_by_state{state}` alongside. Remaining risk: two metrics describe
the same thing. **I would change this** given ownership of the downstream consumers plus a
deprecation window.

**(b) Deploy to ephemeral Compose rather than a cluster.** The alternative was Kubernetes
manifests validated only with `--dry-run`. A deploy that runs, verifies and rolls back is worth
more than manifests nobody applied. Given up: rolling updates, replicas, a real scheduler.
**I would change this** once a staging cluster and credentials exist — `deploy.sh` is the only
file that moves.

## 5. Actual Time Spent

- **Actual time spent:** ~1.5 h wall-clock, one session (transcript timestamps 16:45Z onward).
- **Deliberately left out:** multi-arch images (CI builds `linux/amd64`; the 15 MiB budget was
  verified on `arm64` locally, and QEMU cross-builds would have blown the 10-minute budget);
  alerting rules; graceful shutdown; staging→production promotion. No bonus item was attempted —
  the core loops were worth more than a sixth artifact.
- **Next 60 minutes:** a triggerable alert on the 4xx ratio, demonstrated firing and recovering,
  since the error-ratio panel is currently trusted without ever having been seen to fire.

## 6. Use of AI

Claude Code (CLI), model `claude-opus-5`. Every prompt and visible response is committed:

| File | Session |
|---|---|
| `deploy/ai-transcripts/2026-09-09-claude-code-f25b4b70.md` | short setup session |
| `deploy/ai-transcripts/2026-09-09-claude-code-d981d324.md` | the full implementation session |

Exported from the Claude Code session logs by `scripts/export-ai-transcript.py`, in chronological
order, with sensitive values replaced by `[REDACTED: reason]`.

**Output I rejected.** The first draft of `metrics.go` shipped tuned latency buckets with a
comment stating they "were chosen from the distribution actually measured via
`scripts/loadgen.sh`" — before any measurement had been taken. The numbers were plausible and the
justification was fabricated. I replaced it with `prometheus.DefBuckets` and a comment saying the
boundaries were a placeholder pending real data, then ran the experiment in §3 and re-picked them
from the measurement. Unreviewed, it would have claimed evidence that did not exist — and worse,
the histogram would have been *accidentally* right, hiding the bucket-resolution bug entirely.

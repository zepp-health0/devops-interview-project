# Implementation Notes and Decision Record

## 1. Key Assumptions

1. **The deployment target may be ephemeral.** No Kubernetes exists here, so `deploy` rolls out
   with Docker Compose, on the CI runner and locally. With a real cluster only the compose file
   and the `docker compose` lines in `deploy/deploy.sh` change; digest-pinning and smoke-testing
   do not.
2. **In-memory state is acceptable.** `MemoryStore` loses data on restart, so a deploy is a data
   reset — hence the smoke test creates and deletes its own task. A real datastore needs a
   migration gate.
3. **The existing tests are a contract.** Task IDs start at 0 and `handler_test.go` string-matches
   `task_api_tasks_total 2`. Both look like defects; both are load-bearing. Treated as spec — §4.

## 2. Delivery Path

PR → `verify` (gofmt, `go vet`, golangci-lint, `go test -race`, build) → `image` (builds,
**publishes nothing**, enforces the size budget, asserts healthy and non-root). A PR is untrusted
input, so it gets no registry credentials.

Merge to `main` → `verify` → `publish` (GHCR, tagged `sha-<commit>`, labelled
`org.opencontainers.image.revision`) → `deploy` (`deploy/deploy.sh`).

Artifact identity is the **digest**: `deploy` consumes `needs.publish.outputs.digest`, never
`:latest`, since a tag can be moved. `task_api_build_info` exposes the same revision at runtime,
so the dashboard shows which commit serves traffic.

**Rollback unit: the image digest.** `deploy.sh` records the running image before rollout and
restores it if verification fails. Nothing else changes.

Credentials are `GITHUB_TOKEN` only — minted per run, `packages: write` on the publish job alone.
No PAT is stored.

**Executed, not just authored.** The whole path ran end-to-end: a PR run executed `verify` +
`image` and published nothing, then merge `fe25e05` published
`ghcr.io/byroncustodio/task-api:sha-fe25e05…` at digest `sha256:9185844b…`, and `deploy` pulled
that digest, reached healthy and passed the smoke test. Longest path **239 s** against 600 s.

**Validation boundary.** Those runs were in a *private* mirror, so they are not verifiable from
this PR; and a fork PR gets a read-only token by design, so `publish`/`deploy` skip here and only
`verify` + `image` run. The deployed environment is ephemeral too — a real rollout of a real
artifact, not a long-lived URL.

**Task 1 evidence** (`make image-verify` re-checks all three): `docker image inspect` reports
**3,926,139 bytes**, Docker reports `healthy`, the process runs as **UID 65532**. The measurement
is store-dependent — containerd returns the *compressed* total, the classic store ~14.1 MB
uncompressed. Both pass, but the classic-store margin is only ~10%.

## 3. One Actual Validation or Investigation

**Question:** are the reported latency percentiles true? An in-memory map should answer in tens
of microseconds.

**Observed:** `histogram_quantile` gave **p50 2.5 ms, p95 4.75 ms, p99 4.95 ms**; `sum/count`
gave a true mean of **0.176 ms** — a ~28× discrepancy.

**Isolating it.** Either the service is slow, the experiment is wrong, or the instrument is.
- Raw buckets for `GET /tasks/{id}` showed **all 55 observations in the first bucket** (every
  cumulative count identical). `DefBuckets` starts at 5 ms, so `histogram_quantile` could only
  interpolate inside `[0, 0.005]`.
- An independent client measurement (`curl -w '%{time_total}'`, 30 requests) gave p50 1.9 ms
  *including* process startup and TCP connect — an upper bound that still excluded 4.95 ms.

The service was fine; the instrument was wrong. **Change:** re-bucketed from the measured
distribution, starting at 25 µs (`metrics.go`).

**Re-validation caught a second problem.** The re-run still reported p99 ≈ 4.96 ms —
methodological: `rate(...[5m])` spanned *both* bucket schemas, so `sum by (le)` merged two
incompatible histograms. On a clean TSDB: **p50 0.163, p95 0.446, p99 0.697 ms** vs a 0.173 ms mean.

**Signal confirmed further:** `GET /metrics` is the slowest route at **3.17 ms**, 15–108× any
business route, and scraped every 5 s forever. **112 of its 185 series are latency buckets** — my
re-bucketing grew that from 84. Resolution is paid for on every scrape. Hence the dashboard
excludes `/metrics` and `/healthz` from business panels.

**A second defect surfaced:** the 5xx panel rendered *empty*, not zero — `sum(rate(...))` over a
non-existent series returns an empty vector, making "no errors" indistinguishable from "broken
metric". Fixed with `or vector(0)`.

## 4. Two Engineering Trade-offs

**(a) Kept a metric name that violates Prometheus convention.** `task_api_tasks_total` is a gauge
ending in `_total`, a suffix reserved for counters. Renaming breaks `handler_test.go` and any
existing dashboard or alert, so I kept the legacy names and added a correctly-named
`task_api_tasks_by_state{state}` alongside. Risk: two metrics describe one thing. **I would change
this** given ownership of downstream consumers plus a deprecation window.

**(b) Deploy to ephemeral Compose rather than a cluster.** The alternative was Kubernetes
manifests validated only with `--dry-run`. A deploy that runs, verifies and rolls back beats
manifests nobody applied. Given up: rolling updates, replicas, a scheduler. **I would change
this** once a staging cluster exists — `deploy.sh` is the only file that moves.

## 5. Actual Time Spent

- **Actual time spent:** ~2.5 h wall-clock, one session (transcript timestamps 16:45Z onward).
- **Left out:** multi-arch images (CI builds `linux/amd64`; the budget was verified on `arm64`
  locally, and QEMU cross-builds would blow the 10-minute limit); alerting rules; graceful
  shutdown; staging→production promotion. No bonus item — the core loops were worth more.
- **Next 60 minutes:** a triggerable alert on the 4xx ratio, shown firing and recovering — that
  panel is currently trusted without ever having been seen to fire.

## 6. Use of AI

Claude Code (CLI), model `claude-opus-5`. Every prompt and visible response is committed:

| File | Session |
|---|---|
| `deploy/ai-transcripts/2026-09-09-claude-code-f25b4b70.md` | short setup session |
| `deploy/ai-transcripts/2026-09-09-claude-code-d981d324.md` | the full implementation session |

Exported from the session logs by `scripts/export-ai-transcript.py`, chronological, with
sensitive values replaced by `[REDACTED: reason]`. A transcript cannot contain the commit that
adds it, so the record ends at its own export; `make transcripts` regenerates it.

**Output I rejected.** The first draft of `metrics.go` shipped tuned latency buckets commented as
"chosen from the distribution actually measured via `scripts/loadgen.sh`" — before any measurement
existed. Plausible numbers, fabricated justification. I replaced them with `prometheus.DefBuckets`
and a note that the boundaries were a placeholder pending real data, then ran the §3 experiment and
re-picked them from the measurement. Unreviewed, it would have claimed evidence that did not exist
— and worse, the histogram would have been *accidentally* right, hiding the bucket bug entirely.

# Implementation Notes & Decision Record

## 1. Key assumptions

**No cloud credentials are provisioned for this exercise.** The CI `deploy` job targets the GitHub Actions runner itself — pull, run, poll `/healthz`, tear down — rather than a persistent host. With a real target, `deploy` would swap to `kubectl apply` / `docker context`, and the rollback unit would shift from "delete the runner container" to "redeploy the previous SHA tag."

**The observability stack is a local demo, not internet-facing.** Grafana runs on default `admin/admin` over plain HTTP. Acceptable per the FAQ ("must I deploy publicly? No"), but not shippable as-is — public exposure needs real auth and TLS first.

**Single-replica, in-memory state is acceptable here.** `MemoryStore` has no persistence; a restart drops all tasks. `Store` is already an interface, so a persistent implementation swaps in without touching `handler.go` — but that's untested, since nothing here has been run against real state loss.

## 2. Delivery path

A PR against `main` runs `validate` (`go vet` + `go test -race`) and `build-image` (build + enforce the 15 MiB / non-root constraints from `.github/workflows/ci.yml`) — no external side effects, so a fork PR is safe to run automatically.

A push to `main` runs those same two jobs plus `publish` (tag `ghcr.io/cliffseriex/devops-interview-project` with both `:<commit-sha>` and `:latest`, using the ephemeral `GITHUB_TOKEN` — no stored PAT) and `deploy` (pull the image **by that exact SHA tag**, run it, smoke-test `/healthz`, tear down). The image is built exactly once in `build-image` and passed to `publish`/`deploy` via `actions/upload-artifact`, so the bits that passed the constraint check are the bits that get published — not a second, potentially-different build.

Real run, all four jobs green in under 3 minutes: `https://github.com/cliffseriex/devops-interview-project/actions/runs/32342047654` (commit `6355504`). The `deploy` job's own `docker pull ghcr.io/.../devops-interview-project:6355504` succeeding is the traceability proof — a wrong or missing tag would have failed that step.

**Rollback unit**: one image tag. Every commit on `main` produces an immutable `:<sha>` image; rolling back means re-running the deploy step against the previous SHA tag — no rebuild required.

**Validation boundary**: the "deploy" target is the CI runner, not a long-lived host — documented here per the README's guidance for when an external environment isn't available.

## 3. One real investigation (Task 3C)

**Expectation, written before running `scripts/generate-traffic.sh`**: a mix of successful creates/reads/updates/deletes plus deliberate 400s (missing title, every 5th create) and 404s (unknown id, every 7th read) should move the "request rate by status" panel across five status codes, keep "error rate" flat at 0 (the app has no 5xx path), show P50/P95/P99 in the low-millisecond range (in-memory store, no I/O), and shift `task_api_tasks_state` from all-pending toward some `done` as the script's "mark done" calls land.

**Result — first run, ~45 requests**: request-count panel matched (200/201/400/404/204 all present), but `task_api_tasks_state{state="done"}` stayed at 0 despite scripted updates, and nearly every `GET/PUT/DELETE /tasks/{id}` call showed as `route="unmatched", status="404"` instead of `route="/tasks/{id}"` — a clear mismatch against the "task state should move" and "route should be labeled correctly" expectations.

**Anomaly investigation — service vs. observability vs. experiment**: to isolate which layer was wrong, I ran a manual `curl http://localhost:8080/tasks/0` directly, outside the script: it returned `200` and showed up in `/metrics` correctly labeled `route="/tasks/{id}"`. That ruled out both the app and the metrics middleware — the remaining suspect was the traffic-generation script itself. It used bash-style `${ids[0]}` to reference the first captured task ID, but the shell running these commands is **zsh**, where arrays are 1-indexed by default, so `ids[0]` was silently empty and every "act on an existing task" call hit `/tasks/` (no ID) instead of a real one — a bad experiment, not a service or observability defect.

**Decision and rerun**: rather than patch the symptom (swap `ids[0]` → `ids[1]`) and move on, I rewrote the script (`scripts/generate-traffic.sh`) to use a plain temp file as a FIFO queue instead of a shell array at all, so it can't regress under either bash or zsh indexing. Reran against a reset store: results matched expectations exactly — correct route labels, `400`/`404` at the scripted rate, `tasks_state{done}` incrementing on real updates, P50/P95/P99 sub-5ms.

**Signal selected for further confirmation — error rate**: I picked this one deliberately because it's the panel on-call would trust *not to page falsely*, so a wrong "no error" reading is worse than a wrong "error" reading. Confirming it meant checking what the query returns when zero 5xx have ever occurred, not just when they have. It returned an **empty vector**, not `0` — Prometheus doesn't materialize a time series for a label combination that's never been observed. An empty vector renders as a blank panel in Grafana, which is visually indistinguishable from "the datasource is broken," exactly the kind of false alarm this signal shouldn't produce. Fixed with `... or vector(0)` in `monitoring/grafana/provisioning/dashboards/task-api.json`, confirmed the query now returns `0` instead of empty.

## 4. Two engineering trade-offs

**Distroless + self-check healthcheck vs. a debug-friendly base.** Constraint: under 15 MiB, Docker must actually report `healthy`. Alpine + curl makes `HEALTHCHECK` trivial but costs headroom that mattered once `client_golang` added ~4 MiB (7.8 → 11.7 MiB). Chose `distroless/static-debian12:nonroot` plus a `-healthcheck` flag on the same binary, since `HEALTHCHECK CMD` needs to work without a shell. Cost: no `docker exec sh` for live debugging — if that bit, I'd add the distroless `:debug` variant as a companion image, not revert the prod one.

**Runner-local deploy vs. no real deploy step.** Constraint: no cloud credentials, but the assignment forbids `echo`/pseudocode deploys. Made `deploy` real but scoped to the runner (pull → run → health-poll → teardown). Risk: proves the artifact boots, says nothing about a real target's networking or limits. Given cloud access, I'd point this job there and keep the runner version as a pre-deploy smoke test.

## 5. Actual time / unfinished / next steps

- **Time spent**: ~3 hours across Tasks 1–3, verified against a live Docker daemon and a real GitHub Actions run rather than written and assumed correct.
- **Deliberately not done**: image vulnerability scanning, graceful shutdown, an alert rule with a fire/recover cycle, staging→production promotion — none attempted, to keep the core checklist solid rather than spreading thin.
- **GHCR listing not independently verified via API** — the PAT here wasn't scoped for `packages:read`. Relied on `deploy`'s successful `docker pull` by SHA tag instead, which is arguably stronger evidence (the actual consumer, not just a listing).
- **Next 60 minutes**: graceful shutdown (SIGTERM + drain) paired with a readiness endpoint, since the healthcheck plumbing already exists; then one alert rule (5xx rate > 5% for 5m) with a deliberately triggered and recovered test.

## 6. AI collaboration

The first draft of `scripts/generate-traffic.sh` used bash-style array indexing (`${ids[0]}`), wrong for this environment's zsh shell (1-indexed arrays) — see Section 3 for the full isolation. Caught it via a manual `curl` outside the script rather than trusting the dashboard, then rewrote the script to avoid shell arrays entirely (a temp-file queue) instead of just swapping the index — fixing the bug class, not the instance.

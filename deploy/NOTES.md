# Implementation Notes & Decision Record

## 1. Key assumptions

**No cloud credentials are provisioned for this exercise.** The CI `deploy` job therefore targets the GitHub Actions runner itself — pull the just-published image, run it, poll `/healthz`, tear down — rather than a persistent remote host. If a real target (VM, k8s namespace) were available, `deploy` would swap to `kubectl apply` / `docker context` against it, and the rollback unit would shift from "delete the runner container" to "redeploy the previous SHA tag."

**The observability stack is a local demo, not internet-facing.** Grafana runs on default `admin/admin` over plain HTTP (`docker-compose.yml`). That's acceptable per the assignment FAQ ("must I deploy to a public environment? No"), but it is not shippable as-is — any public exposure needs real auth and TLS first.

**Single-replica, in-memory state is acceptable for this assignment.** `MemoryStore` (`store.go`) has no persistence; a restart drops all tasks. The `Store` interface already isolates this, so a persistent implementation can be swapped in without touching `handler.go`, but nothing here has been built or tested against real state loss.

## 2. Delivery path

A PR against `main` runs `validate` (`go vet` + `go test -race`) and `build-image` (build + enforce the 15 MiB / non-root constraints from `.github/workflows/ci.yml`) — no external side effects, so a fork PR is safe to run automatically.

A push to `main` runs those same two jobs plus `publish` (tag `ghcr.io/cliffseriex/devops-interview-project` with both `:<commit-sha>` and `:latest`, using the ephemeral `GITHUB_TOKEN` — no stored PAT) and `deploy` (pull the image **by that exact SHA tag**, run it, smoke-test `/healthz`, tear down). The image is built exactly once in `build-image` and passed to `publish`/`deploy` via `actions/upload-artifact`, so the bits that passed the constraint check are the bits that get published — not a second, potentially-different build.

Real run, all four jobs green in under 3 minutes: `https://github.com/cliffseriex/devops-interview-project/actions/runs/32342047654` (commit `6355504`). The `deploy` job's own `docker pull ghcr.io/.../devops-interview-project:6355504` succeeding is the traceability proof — a wrong or missing tag would have failed that step.

**Rollback unit**: one image tag. Every commit on `main` produces an immutable `:<sha>` image; rolling back means re-running the deploy step against the previous SHA tag — no rebuild required.

**Validation boundary**: the "deploy" target is the CI runner, not a long-lived host — documented here per the README's guidance for when an external environment isn't available.

## 3. One real investigation

Validating Task 3C (`docker-compose.yml` + `monitoring/`), I generated ~45 mixed CRUD requests locally and expected the dashboard's request/latency/task-state panels to reflect them. Instead, `task_api_http_requests_total` showed nearly all `GET/PUT/DELETE /tasks/{id}` calls as `route="unmatched", status="404"`, and `task_api_tasks_state{state="done"}` stayed at 0 despite several "mark done" calls in the script.

To isolate app bug vs. instrumentation bug vs. bad experiment, I ran a manual `curl http://localhost:8080/tasks/0` directly: it returned `200` and showed up in `/metrics` correctly labeled `route="/tasks/{id}"`. That ruled out the app and the metrics middleware. The remaining suspect was the traffic-generation script itself — it used bash-style `${ids[0]}` to reference the first captured task ID, but the shell running these commands is **zsh**, where arrays are 1-indexed by default, so `ids[0]` was silently empty and every "act on an existing task" call hit `/tasks/` (no ID) instead of a real one.

I fixed the script (`ids[1]` instead of `ids[0]`), reset the in-memory store, and reran: results matched expectations exactly — 200s on valid IDs, the scripted 9/45 `400`s and 6/45 `404`s at the expected rate, `tasks_state{done}` incrementing on real updates, P50/P95/P99 in the low-millisecond range (expected for an in-memory store with no I/O). I did not change the application or the metrics code — the evidence pointed entirely at the test harness. Separately, this run also caught a real dashboard bug: the error-rate panel's query returned an *empty* vector (not `0`) when no 5xx had ever occurred, which renders as a blank panel indistinguishable from "broken." Fixed with `... or vector(0)` in `monitoring/grafana/provisioning/dashboards/task-api.json`.

## 4. Two engineering trade-offs

**Distroless + self-check healthcheck vs. a debug-friendly base.** Constraint: image must be under 15 MiB and Docker must actually report `healthy`. Alpine + curl makes `HEALTHCHECK` trivial but costs size headroom that mattered once `client_golang` added ~4 MiB (7.8 → 11.7 MiB). I chose `gcr.io/distroless/static-debian12:nonroot` plus a `-healthcheck` flag baked into the same Go binary, so `HEALTHCHECK CMD ["/task-api", "-healthcheck"]` works without a shell. Cost: no `docker exec sh` for live debugging. If that became a real blocker, I'd add the distroless `:debug` variant as a manually-pulled companion rather than reverting the prod image.

**Runner-local deploy vs. no real deploy step.** Constraint: no cloud credentials for this exercise, but the assignment forbids `echo`/pseudocode deploy steps. I made `deploy` real but scoped to the runner (pull → run → health-poll → teardown) rather than skip or fake it. Risk: this proves the artifact boots and passes its own health check, but says nothing about a real target's networking or resource limits. Given real cloud access, I'd point this same job at that target and keep the runner-local version as a pre-deploy smoke test.

## 5. Actual time / unfinished / next steps

- **Time spent**: ~3 hours across Tasks 1–3, verified against a live Docker daemon and a real GitHub Actions run rather than written and assumed correct.
- **Deliberately not done**: image vulnerability scanning, graceful shutdown, an alert rule with a fire/recover cycle, staging→production promotion — none attempted, to keep the core checklist solid rather than spreading thin.
- **GHCR listing not independently verified via API** — the PAT here wasn't scoped for `packages:read`. Relied on `deploy`'s successful `docker pull` by SHA tag instead, which is arguably stronger evidence (the actual consumer, not just a listing).
- **Next 60 minutes**: graceful shutdown (SIGTERM + drain) paired with a readiness endpoint, since the healthcheck plumbing already exists; then one alert rule (5xx rate > 5% for 5m) with a deliberately triggered and recovered test.

## 6. AI collaboration

Claude wrote the local traffic-generation script used to validate Task 3C with bash-style array indexing (`${ids[0]}`), which is wrong for this environment's zsh shell (1-indexed arrays) — see Section 3. It self-caught this via a manual isolation test rather than assuming the dashboard was correct, then fixed the script and reran to confirm. This is the most concrete example from this session: a tool-generated artifact was wrong in a way that would have produced a misleading validation result if not checked against a second, independent method (a raw manual `curl`).

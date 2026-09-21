# Review of AI output — rejected, changed, corrected

Cross-referenced from `deploy/NOTES.md` §7. Each entry names the exchange in
`01-claude-session-2026-09-14.md` where it happened.

## Rejected

**Fabricated bucket justification** (Response 26, corrected across 45–52).
The first `metrics.go` shipped histogram buckets with a comment reading
"tuned for an in-memory API: p50 lands in the hundreds of microseconds." No
measurement existed at that point. The numbers were plausible and the reasoning
was invented. Had it been accepted, the histogram might have been accidentally
right — the final measured p50 was within 5µs of the fabricated one — which
would have hidden the bucket-floor bug entirely. NOTES.md §3 is the result of
replacing that assertion with data: five traffic runs, two bucket revisions,
and a control query.

**`RegisterStoreMetrics` on the default registry** (Response 11, deleted at
Response 13). The model proposed a helper registering into
`prometheus.DefaultRegisterer`. That registry is process-global; constructing
metrics twice in a test run panics on duplicate registration. Replaced with a
private `prometheus.NewRegistry()` per `Metrics`.

**UPX compression** (Responses 21–22). Added on request, then removed on
request. The binary was 10,895,549 bytes against a 15,728,640 limit. Packing
would add decompression to every healthcheck exec, raise RSS, and trip scanners
that flag packed binaries — for margin that was not needed.

## Changed

**`store.Count()` / `store.CountDone()`** (Response 13, fixed at 31). Suggested
before the model had read the `Store` interface. They do not exist; the
interface exposes `Stats() (total, done int)`, which is exactly what
`NewMetrics` takes. All call sites changed to `store.Stats`.

**`HealthHandler` signature** (Responses 32–33). The model's `main.go` and
`metric_test.go` assumed the plain upstream signature while `handler.go` had a
two-arg factory. Kept the factory; fixed the two call sites; added
`TestHealthzReportsBuild` so a broken `-ldflags -X` wiring cannot produce a
healthy-looking `/healthz` reporting `dev`.

**`aquasecurity/trivy-action@0.28.0`** (Response 53). A version number from
memory that does not exist. Replaced with a direct `docker run` of a pinned
`aquasec/trivy` image, which also makes CI match `make scan`.

## Corrected — model misdiagnoses

**"Empty scrape"** (Responses 35–37). The model twice asserted the scrape body
was empty and diagnosed a registry or handler fault. The body was populated;
`head -30` was truncating before `task_api_*`, which sorts after `go_*`. The
model had not asked to see the full output before diagnosing.

**`grep -c` as a version check** (Responses 35–36, 50–51). Twice the model used
a line count to decide whether a file was the generated version. The counts
matched commented-out code and a parameter name, not live content. Both times
the conclusion drawn from the count was wrong.

**"Stale image" → "multi-arch summation" → containerd double-count**
(Responses 46, 47, 57, 59). Three theories for why `docker image inspect`
reported 15.27 MB against a 10.9 MB binary. The first (stale tag) was
disproved by a clean rebuild. The third (three platforms in a manifest list)
was disproved by a single-platform build returning the same figure. The
second — Docker 29's containerd store counting both the unpacked snapshot and
the compressed blob — was right, and the model had retracted it prematurely.
The arithmetic that confirmed it: 10,899,582 + 4,332,484 + two ~12 kB layers +
config/manifest ≈ 15,277,160.

**`health: "unknown"`** (Response 49). Not a model misdiagnosis of the app, but
a defect in the model's own `verify.sh`: it waited for container health and not
for the first Prometheus scrape, so a cold start reported both targets
`unknown`. Earlier runs passed only because the stack was warm. Fixed by
polling the targets API.

**Transcript scaffold committed as transcript** (Responses 61–70, flagged by
the reviewer 2026-09-20). The model declined to write a transcript from memory
and supplied a scaffold with `«paste verbatim»` placeholders. The scaffold was
committed; the content was not. This directory is the correction.

## Not changed, deliberately

**`NaN` for idle routes** (Response 45). `PUT /tasks/{id}` returns `NaN` for
the per-route mean when no PUT falls inside the `rate()` window. Suppressing it
with `or vector(0)` would claim a measurement that was never taken. Left as is.

**Kept `prometheus/client_golang`** (Response 22). Hand-rolling exposition
format would reach ~3 MB but means maintaining bucket accumulation, label
escaping, and concurrency-safe counters by hand — and §3 shows bucket
boundaries are already the subtle part.

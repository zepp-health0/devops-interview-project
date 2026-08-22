# Implementation Notes & Decision Record

## 1. Key assumptions

- No cloud creds for this exercise. The `deploy` job just pulls the image, runs it, checks `/healthz`, kills it — not a real host. With an actual target I'd swap that for `kubectl apply` or similar, and rollback becomes "redeploy the previous SHA tag" instead of "delete the runner container."
- Grafana's on default `admin/admin`, plain HTTP. Fine for local (README says public deployment isn't required) but I wouldn't ship it like this.
- Store is in-memory, restart wipes it. `Store` is already an interface so swapping in something persistent later is easy, but I haven't actually tested that path — just assuming it works.

## 2. Delivery path

PR → `main` runs `validate` (vet + test) and `build-image` (build, then check size/non-root in CI). No side effects, safe for a fork PR.

Push → `main` runs those same two plus `publish` (GHCR, tagged with the commit SHA and `latest`, uses `GITHUB_TOKEN` — nothing stored) and `deploy` (pulls by SHA, runs it, polls `/healthz`, tears down).

The image is built once in `build-image` and handed to publish/deploy as an artifact, so what gets published is literally what passed the constraint checks, not a second build that could've drifted.

Actual PR into this repo: https://github.com/zepp-health0/devops-interview-project/pull/2 (branch `cliffseriex:main`). Its CI run shows `action_required` — GitHub gates Actions runs from first-time external contributors behind maintainer approval on public repos, so it won't execute until someone with write access here clicks approve. That's the platform working as intended, not a failure on my side, and it's also a live example of the same "what side effects should a PR be allowed" question Task 2 asks about — a fork PR shouldn't get to run unsupervised against someone else's repo.

Since I only have read access here, I can't push to this repo's own `main` to demonstrate the `publish`/`deploy` path directly. I proved that path end-to-end on my own fork instead: all 4 jobs green in under 3 minutes, `github.com/cliffseriex/devops-interview-project-scratch/actions/runs/32342047654` (commit `6355504`, same code as this PR). `deploy` there successfully pulling the image by that exact SHA tag is the traceability proof — wrong tag, that step fails.

Rollback unit is one image tag — every commit gets an immutable image, rolling back is just pointing deploy at the previous one.

Worth being upfront: "deploy" targets the CI runner, not a persistent host, because there's no cloud environment for this exercise.

## 3. One real investigation (Task 3C)

Before running the traffic script I wrote down what I expected: all 5 status codes showing up on the request panel, error rate flat at 0 (app has no 5xx path), latency in low ms, task state shifting from all-pending to some done.

First run (45 requests): request counts looked right, but task state never moved, and almost every `/tasks/{id}` call showed up as `route="unmatched", status="404"` instead of hitting the route properly.

Ran a manual `curl /tasks/0` outside the script to check — worked fine, 200, correctly labeled in `/metrics`. So the app and the metrics code were fine, it had to be the script. Turned out it used `${ids[0]}` to grab the first task id — this shell is zsh, which is 1-indexed, so that was silently empty and every request was hitting `/tasks/` with no id at all.

Fixed the script properly (temp file instead of an array, so it can't happen again under either shell) and reran. Everything matched this time: correct routes, 400/404 at the rate the script targets, task state moving, latency sub-5ms.

Also dug into the error-rate panel specifically, since a false "no errors" reading is the worst kind of wrong for on-call to trust. The query returned an empty result (not `0`) when there'd never been a 5xx — renders as a blank panel, looks identical to "broken." Added `or vector(0)` to fix it.

## 4. Two trade-offs

**Distroless + a `-healthcheck` flag on the binary**, instead of Alpine + curl. Distroless has no shell, so `HEALTHCHECK` can't exec anything except the app itself — hence the flag. Costs debuggability (no `docker exec sh` into a running container). I'd reconsider if that actually became a problem in practice — add the distroless debug variant as a separate image rather than switch the main one back.

**Deploy only targets the CI runner**, not a real host — no cloud creds available, but the assignment rules out a fake/echo deploy step, so I made the runner-local version real (pull, run, health check, teardown) instead of skipping it. Doesn't prove anything about a real target's networking or resource limits. Given actual cloud access I'd point this same job there and keep the runner version as a pre-deploy smoke test.

## 5. Time / unfinished / next

- 3 hours across tasks 1–3.
- Didn't do: image scanning, graceful shutdown, an alert with a real fire/recover test, staging→prod promotion. Skipped to keep the core stuff solid instead of spreading thin.
- Couldn't verify the GHCR package listing through the API — token wasn't scoped for `packages:read`. `deploy`'s successful pull is decent evidence either way.
- Next hour: graceful shutdown + a readiness probe (healthcheck plumbing's already there), then one alert rule with an actual triggered/recovered test.

## 6. AI collaboration

Used Claude for most of this build. Concrete example: the traffic-generation script it wrote used `${ids[0]}`, which is wrong under zsh (see section 3) — caught by testing a request manually outside the script instead of trusting what the dashboard showed. Fixed it properly (temp file instead of array) rather than just changing the index, since the same 0-vs-1-indexing assumption could've bitten anywhere else in the script too.

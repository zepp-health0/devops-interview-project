# Implementation Notes and Decision Record

## 1. Key assumptions

1. Local Docker Desktop is available for validation and is a reasonable deployment boundary for the interview. This is required for `docker build`, `docker compose up`, and the container health checks in `Dockerfile` and `docker-compose.yml`. If the target were a remote runner or ECS/Kubernetes cluster, the same app artifacts would still be valid, but the local runtime boundary would need to be re-validated.
2. Prometheus and Grafana are sufficient for a minimal, demonstrable observability stack for this app. That assumption is encoded in `monitoring/prometheus.yml`, `monitoring/grafana/provisioning/*`, and the dashboard in `monitoring/grafana/dashboards/task-api-overview.json`. If the deployment needed long-term retention or alert routing, we would add remote storage and alert rules, but the current stack is enough to prove the app is observable.


## 2. Delivery path

PRs and pushes to `main` trigger validation in `.github/workflows/ci.yml`:
- `validate`: `go vet` and `go test -race -count=1 ./...`
- `container`: `docker build -t task-api:${{ github.sha }} .` and a smoke test that waits for `/healthz` and checks Docker health status
- `publish`: only on `push` to `main`, pushes `ghcr.io/<owner>/task-api:${{ github.sha }}` and `latest`
- `deploy`: only on `push` to `main`, runs `docker compose -f docker-compose.yml up -d --force-recreate --build` and then `curl -fsS http://127.0.0.1:8080/healthz`

The rollback unit is the current container image plus the compose stack; in this repo the smallest rollback is to redeploy the previous image tag or rerun the compose stack against the earlier image. The image is identified by commit SHA in GHCR and by the local `task-api:local` tag for Docker Compose validation.

## 3. One actual validation

I validated the runtime health and observability path using the actual Docker and Prometheus/Grafana stack.

Expected: the app container should become healthy, answer `/healthz`, and produce Prometheus metrics that Grafana can query.

Commands run:
- `docker build -t task-api .`
- `docker image inspect task-api --format '{{.Size}}'` -> `8737372` bytes
- `docker run -d --name task-api-test -p 8080:8080 -e PORT=8080 task-api` then `curl -fsS http://127.0.0.1:8080/healthz` -> `{"status":"ok"}`
- `docker inspect --format '{{.State.Health.Status}}' task-api-test` -> `healthy`
- `docker compose up -d --build --force-recreate`
- `curl -fsS http://127.0.0.1:9090/api/v1/targets` -> target health `"up"`
- `curl -u admin:admin http://127.0.0.1:3000/api/search` -> dashboard `Task API Overview` is present
- `curl -u admin:admin -G --data-urlencode 'query=task_api_tasks_total' http://127.0.0.1:3000/api/datasources/proxy/1/api/v1/query` -> a live vector result was returned, proving Grafana can read Prometheus data.

I also generated task traffic (`POST /tasks`, `PUT /tasks/{id}`, `GET /tasks`, `DELETE /tasks/{id}`) and confirmed the dashboard-relevant PromQL worked: `task_api_tasks_total` reported `4` after 5 task actions and `histogram_quantile(0.95, sum by (le) (rate(task_api_task_request_duration_seconds_bucket[5m])))` returned `0.0095` seconds.

## 4. Two engineering trade-offs

1. I chose alpine over scratch and distroless because those ultra-minimal images completely lack a shell and the wget utility. Without these tools, they cannot successfully execute the command, causing the health check to fail blindly. alpine provides the necessary environment to report an accurate, live health status while maintaining a tiny footprint.


## 5. Actual time spent

- Actual time spent: about 3.5 hours total across repo review, patching, container validation, and observability verification.
- Work intentionally left out: adding alert rules, artifact promotion beyond a local deploy, and cloud deployment beyond the local Docker runtime required by the repository.
- Next 60 minutes: add a tiny alert rule for 5xx rate > threshold, tighten the dashboard to include a 24h view, and add one more smoke-test for a failed task update or 404 edge case.

## 6. Use of AI

Yes

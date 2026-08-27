# DevOps Interview Project

[Chinese](README.zh-CN.md) | English

> Suggested effort: 2–3 hours | AI tools are allowed

## Background

This repository contains a simple Go REST API for creating, reading, updating, and deleting tasks. It also exposes `/healthz` and `/metrics`. The current repository is only a locally runnable starting point.

Your goal is to move it toward an engineering solution that is **buildable, deliverable, observable, and diagnosable** within the time you choose to invest. We care not only about whether the final files exist, but also about how you identify problems, prioritize work, validate results, and handle unfinished areas.

## Working Guidelines

- The suggested effort is **2–3 hours**, not a hard limit. You may spend more time; record your actual time, unfinished work, and next steps honestly in `deploy/NOTES.md`.
- The assignment does not provide every detail of a production environment. When information is missing, record the key assumptions you need and continue; you do not need to wait for the interviewer to provide a single correct answer.
- Tasks 1–5 are parallel evaluation dimensions, and you may choose the order. Steps 3A–3C are only the internal validation sequence for the observability task.
- You may use ChatGPT, Claude, Copilot, Cursor, or other tools, but you must be able to explain, run, and modify every part of your submission. If you use AI, you must also commit the complete record described in [AI Conversation Records](#ai-conversation-records).
- You do not need to purchase cloud resources. The deployment target may be a local or remote environment that you can demonstrate.
- No specific tool or implementation is required. Any approach is acceptable if it satisfies the behavioral constraints below and you can explain your choices.
- The interview will use your submission directly for validation and live changes. It will not focus on memorizing tool-specific knowledge.

## Key Files

```text
.
├── main.go
├── handler.go
├── handler_test.go
├── store.go
├── store_test.go
├── Dockerfile
├── Makefile
├── docker-compose.yml
├── .github/workflows/ci.yml
├── monitoring/prometheus.yml
├── deploy/NOTES.md
└── deploy/COST.md
```

Local baseline:

```bash
go test -race -count=1 ./...
go run .
```

API:

```text
GET    /healthz
GET    /metrics
GET    /tasks
POST   /tasks
GET    /tasks/{id}
PUT    /tasks/{id}
DELETE /tasks/{id}
```

## Task 1: Containerization

Package the application as a runnable container image.

**Acceptance outcomes:**

- `docker build -t task-api .` succeeds;
- after the container starts, `/healthz` returns `{"status":"ok"}`;
- Docker actually reports the container as `healthy`; merely having a `HEALTHCHECK` instruction in the Dockerfile is not sufficient;
- the final image is smaller than 15 MiB, measured using the byte value returned by `docker image inspect task-api --format '{{.Size}}'`;
- the application process does not run as root.

The current Dockerfile is only a starting point. Inspect the resulting artifact and its runtime behavior yourself.

## Task 2: CI/CD

Implement a pipeline that can validate, publish, and deploy this project. You may use GitHub Actions or another platform.

**Behavioral constraints:**

- pushes to `main` and pull/merge requests targeting `main` trigger the necessary automated validation;
- the overall delivery design covers static analysis, tests, compilation, container image build, image publication, and deployment, but not every event needs to execute every stage;
- you decide which external side effects are allowed for pull/merge requests, pushes to `main`, manually approved events, or equivalent events on your chosen platform;
- published images can be traced to a specific commit;
- credentials do not appear in plaintext in the repository or logs;
- deployment is not an `echo`, comment, or pseudocode: it must include actual commands that create or update a runtime environment. The target may be local, temporary, or remote, and it does not need to expose a long-lived public URL;
- the actual execution time target for jobs on the automated validation path is under 10 minutes, excluding runner queueing, manual approval, and waiting for external environments.

If publication or deployment cannot run online because of public-fork behavior, credentials, or external-environment limitations, keep an executable implementation with safe conditions, perform local or equivalent end-to-end validation, provide the resulting evidence, and document the validation boundary in `deploy/NOTES.md`.

## Task 3: Observability

### 3A. Run the monitoring stack

Complete `docker-compose.yml` and `monitoring/` so that the application, Prometheus, and Grafana can run together.

**Acceptance outcomes:**

- the Prometheus target is shown as `UP`;
- Grafana can query Prometheus data;
- the repository contains a Dashboard that can be imported or provisioned automatically;
- after starting from a clean environment and generating your documented test traffic, the Dashboard shows real data rather than remaining empty because of a datasource, query, or time-range problem.

### 3B. Make the metrics useful for on-call decisions

The existing metrics are not sufficient to determine whether the API is healthy. Modify the application and Dashboard so that an on-call engineer can answer:

- What is the current request volume and failure behavior?
- What are the P50, P95, and P99 latencies for business requests?
- What is the current task state?

You decide the metric types, labels, statistical boundaries, and sampling approach. Keep `/metrics` as the Prometheus scrape endpoint, and add the necessary tests for new application code.

### 3C. Validate the observations

Use the existing API to generate the business traffic and state transitions that you consider sufficient to validate the Dashboard. You decide the method and operation mix.

Before running the experiment, write down what you expect each relevant panel to do. Afterward:

1. compare the Dashboard and raw queries with your expectations, and record whether the actual changes match;
2. select one signal for further confirmation and explain why it deserves attention;
3. if you observe an anomaly, missing data, or behavior you cannot explain, use additional queries, commands, or experiments to distinguish service behavior, observability implementation, and the experiment itself;
4. use the evidence to decide whether to change the implementation, and record the conclusion and next step in `deploy/NOTES.md`;
5. if you make a change, rerun the relevant steps to validate your conclusion.

We do not expect a large number of panels. A small number of trustworthy signals that help you discover and explain issues is more valuable.

## Task 4: Submit a Decision Record

Complete `deploy/NOTES.md`. It is not a generic DevOps questionnaire; it is an evidence index for this implementation, including:

- the key assumptions on which your implementation depends;
- the actual delivery path and rollback unit;
- one validation or investigation that you actually performed;
- two engineering trade-offs you deliberately made, and the conditions that would cause you to change those decisions;
- actual time spent, unfinished work, and next steps;
- if you used AI, an index of all transcript files and one concrete example of AI output that you reviewed, modified, or rejected.

Reference actual files, jobs, commands, or runtime results. Avoid generic statements such as “in production, we should…”. Keep it concise and aim for no more than roughly 1,000 words.

## Task 5: Cost Scenario Analysis

> A writing task — no code, and nothing to buy. Write your answer in `deploy/COST.md`, up to 1,000 words. If you can get real data, give numbers and the queries you would run; if you cannot, say what you are assuming and how you would check it. We read the reasoning as closely as the conclusion.

### The setup

Think of the `task-api` from Tasks 1–4 as one small service inside a bigger system. That system also has a `user_event` table that takes a lot of writes:

- events are written to **DynamoDB**;
- the data is copied into an **S3** bucket used as a data lake;
- services on **EKS** read those events;
- traffic to the outside goes out through an **ELB**.

The app team recently shipped a big release that changed how the client sends requests.

### What you are looking at

- The monthly AWS bill went from **~$40k to ~$52k**, about **+30%**.
- Business volume (daily active users, core request count) grew about **+5%**.
- The extra cost is spread across DynamoDB, S3, EKS and ELB. Some of it moves with the number of writes; some of it does not move with usage at all.
- Last month had **31 days**. This month has **30**.
- A batch of **Reserved Instances and Savings Plans** was bought at the start of the quarter.
- Those dollar figures are **unblended cost** — the price of each line as it was charged, before any Reserved Instance or Savings Plan discount is averaged out across the account.

### What you can use

- **Cost Explorer** — the console view of spend. You can group it by service, account, region, tag, and usage type (the billing sub-category inside one service, such as write capacity versus stored bytes).
- **The CUR** (Cost and Usage Report) — an export with one row per charge, including the resource ID.
- **Tags** — about **60%** of resources carry `team` / `env` tags. The rest carry none.
- **CloudWatch**, plus whatever service metrics you built in Task 3.

### What we want to know

Finance and your manager are asking three things: **why did it go up, will it come down next month, and what would bring it down.** Answer in `deploy/COST.md`, covering:

**1. Where you start.** What do you look at first to decide whether +30% is even surprising? Pick a unit to measure in — cost per event, per request, per active user — and say why that one. How do you tell "we are simply doing more business" apart from "we are wasting money"?

**2. Where the money went.** Walk through one chain of *what you saw → what you think it is → the exact data that would prove it*, until the 30% is broken into named pieces. On the way, deal with:

- the 40% of resources with no tags — how do you work out who they belong to?
- four services rising at once — one shared cause, or several separate ones, and how would you tell?
- the other explanations you have to rule out before you trust your own: the different number of days in the two months, how those Reserved Instance and Savings Plan payments land in the bill, one-off charges.

**3. What you would do.** Pick one of these situations, give a first step you could start on Monday, and say what you give up by choosing it:

- buying Reserved Instances or Savings Plans would help, but this table is being moved to a different store next quarter;
- the real fix is in the app's request logic, but the app team has no time this quarter;
- your manager wants 30% off the bill, and your own estimate is that only about 15% is really recoverable.

Separate the one-off fix from the thing that keeps the cost from creeping back up, and say how you would keep it from creeping back.

**4. Something you have done before (optional).** One cost cut you actually made: the numbers before and after, and how you convinced yourself the saving came from your change rather than from business volume moving on its own.

Tie each conclusion to a specific piece of data and to how you would check it. A general list of cost-saving tips is not what we are looking for.

## AI Conversation Records

If you use an AI tool at any point, the complete record of that use is part of the required submission.

- Save every prompt and every visible AI response under `deploy/ai-transcripts/`, including conversations whose output you did not use.
- Keep the records in chronological order, use one Markdown, text, or JSON file per session, and identify the tool and model when known.
- If a tool does not support export, copy the visible conversation manually. For inline completion tools with no conversation view, keep a contemporaneous manual log of the instructions or context you provided and the suggestions you accepted, changed, or rejected.
- Do not commit credentials, tokens, personal data, or confidential third-party information. Replace only sensitive values with `[REDACTED: reason]` so the rest of the record remains reviewable.

Transcript files do not count toward the `deploy/NOTES.md` word limit. If you did not use AI, write `Not used` in Section 6 of `deploy/NOTES.md`; no transcript directory is required.

## Core Acceptance Checklist

- [ ] The original Go tests pass, with no data races;
- [ ] the image runs, becomes healthy in practice, is smaller than 15 MiB, and runs the application as a non-root user;
- [ ] CI performs actions appropriate to the risk level of pull/merge requests and pushes to `main`;
- [ ] image publication and deployment are real and traceable;
- [ ] the Prometheus target is `UP`, and the Grafana Dashboard contains real data;
- [ ] metrics can answer questions about traffic, errors, latency percentiles, and task state;
- [ ] the Dashboard has been validated with actual business traffic, actual results have been compared with expectations, and at least one signal has been confirmed further;
- [ ] `deploy/NOTES.md` references real evidence from this submission;
- [ ] `deploy/COST.md` ties the cost increase to data or to stated assumptions, makes one real trade-off under a constraint, and tells a one-off fix apart from one that keeps the cost down;
- [ ] if AI was used, `deploy/ai-transcripts/` contains complete, chronological records of all AI interactions, with only sensitive values redacted.

## Evaluation Focus

| Dimension | What we look for |
|---|---|
| Debugging and validation | Whether you gather evidence first, form hypotheses, control variables, and validate again |
| Engineering judgment | Whether you identify conflicting constraints, make trade-offs, and explain remaining risks |
| Correctness | Whether runtime behavior matches your claims and remains correct when boundaries change |
| Delivery safety | Whether permissions, credentials, publication conditions, and artifact traceability are reasonable |
| Observability | Whether metrics and the Dashboard actually support decisions and troubleshooting |
| Prioritization | Whether you prioritize the highest-value closed loops within the time you actually invest |
| Cost judgment | Whether you measure cost per unit of business, trace the increase back to data, tell a one-off fix apart from one that lasts, and make a workable trade-off under a constraint |

## Optional Bonus

After completing the core work, choose at most **one** item to continue implementing. Not completing a bonus item does not prevent a strong evaluation of the core work.

- container image scanning and a policy for handling the result;
- graceful shutdown with repeatable validation;
- a readiness mechanism with demonstrated state transitions;
- an alert that can be triggered and shown to recover;
- artifact promotion from staging to production;
- a queryable SLI/SLO for this service.

What you choose is less important than why it deserves the remaining time more than the alternatives, and whether you actually validated it.

## FAQ

**May I use AI?** Yes. If you use it, you must commit every prompt and visible response as described in [AI Conversation Records](#ai-conversation-records). You remain responsible for the final result, and during the interview you may be asked to explain or modify AI-generated parts.

**Must I deploy to a public environment?** No. The deployment target must be executable and demonstrable, but it may be local. Do not purchase resources for this assignment.

**Must I stop after 2–3 hours?** No. This is suggested effort, not a hard limit. Regardless of how long you spend, record what you validated, why anything remains unfinished, and what you would do next. Good prioritization and validation matter more than accumulating files.

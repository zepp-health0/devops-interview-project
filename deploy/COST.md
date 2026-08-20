# Cost Scenario Analysis

## 1. Baseline

First normalize, don't compare raw dollars: the headline +30% ($40k→$52k) mixes a 31-day and a 30-day month (~3% mechanical swing) and is unblended, so it also carries the RI/SP purchase's upfront/amortized effect rather than pure usage cost. Recompute both months as **$/day**, then as **$/user_event written** (the one metric that ties every downstream service — DynamoDB, S3, EKS, ELB — back to a shared unit of business volume). If $/event is flat while event volume is +5%, that share of the increase is legitimate scaling, not waste. Anything above what +5% volume predicts, per service, is the efficiency/waste bucket to chase.

## 2. Attribution (observe → hypothesize → verify)

**Observe**: total +30% unblended, volume +5%, spread unevenly across four services.

**Hypothesize**: the app's request-logic change altered the *shape* of usage, not just its volume — e.g. larger DynamoDB item size, extra S3 LIST/GET calls, or a retry/backoff loop inflating ELB request count and processed bytes.

**Verify per service**:
- **DynamoDB**: CloudWatch `ConsumedWriteCapacityUnits`/`ConsumedReadCapacityUnits` and average item size, month over month. Flat request count but rising WCU per write points at payload growth, not volume.
- **S3**: request-count metrics (PUT/GET/LIST), not just stored GB — at high-write/small-object profiles, request pricing usually dominates storage cost.
- **EKS**: pod-hours/node count against the app's own `rate(task_api_http_requests_total[..])` (Task 3's metric) — pod count scaling independently of request rate suggests a stuck HPA or over-provisioning, not real load.
- **ELB**: `ProcessedBytes` and request count — a retry loop shows up here as request count outpacing DynamoDB write count.

**Covering the untagged ~40%**: join CUR `resource_id`/ARN against the known inventory for this one pipeline — a single table, bucket, cluster, and ELB are identifiable by ARN pattern even without `team`/`env` tags.

**Confounders**: billing-day mismatch (normalize to $/day, §1); RI/SP amortization (bought this quarter but explicitly excludes DynamoDB/S3 — check CUR's amortized-cost vs. unused-commitment lines to confirm it only touches EKS-adjacent EC2, so it can't explain a DynamoDB/S3 rise); one-off charges (a backfill job isolated to one week in `line_item_usage_type`).

**Shared root cause or not**: if DynamoDB and S3 scale proportionally with write count but EKS/ELB diverge from it, that's evidence of two separate causes (a payload-size regression *and* an infra-scaling issue) rather than one fix covering everything.

## 3. Trade-off

Constraint ①: RI/SP is attractive for DynamoDB capacity, but this table migrates next quarter — committing now is the wrong bet. **Actionable first step**: switch DynamoDB to on-demand (or right-size provisioned capacity against actual `ConsumedCapacityUnits`) instead of buying a reservation. It's reversible, ships this week, and directly targets the "not proportional to usage" portion. **What I give up**: on-demand's per-request unit cost is higher than a well-utilized reservation, so this can look worse on a unit-cost dashboard even as it removes commitment risk — the trade explicitly favors flexibility over unit-cost optimality given the migration timeline is uncertain.

**Point fix vs. root-cause governance**: the on-demand switch is a point fix — reversible, fast, doesn't touch the app. The actual root cause is the shipped request-logic change (larger items / extra calls / retries); that needs the APP team's capacity, not infra tooling, or cost drifts back up with the next volume bump regardless of how DynamoDB is billed. **Regression guard**: a CloudWatch billing alarm on $/event (using Task 3's event-write-rate metric as the denominator), so a divergence between volume growth and cost growth is caught in days, not a full billing cycle.

## 4. Experience

Skipping — I don't have a real optimization from this codebase to report honestly, and this section is explicitly optional. If you've done a comparable cost investigation before, that's the strongest thing to add here yourself; it's the one part of this document an interviewer is most likely to probe on details a generated example couldn't hold up to.

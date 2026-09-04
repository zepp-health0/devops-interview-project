Write `deploy/COST.md` as a concise, evidence-driven AWS cost investigation.

Scenario: business volume increased only 5%, but total AWS costs increased ~30% after a recent application release.

Follow this structure:

1. **Baseline & Normalization:** Establish service-level unit costs (for example, DynamoDB cost per 1M writes, S3 cost per GB-month, EKS node-hours per business volume, ELB cost per LCU) alongside one consistent business metric. Normalize daily costs and separate confounders such as 30 vs. 31 billing days, RI/SP amortization, and one-time charges using net/unblended cost.

2. **Observe → Hypothesize → Verify:** Trace how one release could create a downstream cost increase across DynamoDB, S3, EKS, and ELB. Investigate write amplification, S3 staging/polling, autoscaler churn/idle capacity, and active connections/LCU. Correlate CUR timestamps with CloudWatch metric step-changes to validate or reject the hypothesis.

3. **Untagged Assets:** Investigate the ~40% untagged resource footprint using account/Region, Resource IDs, and parent-resource relationships. Avoid leaving material spend in a generic “unknown” bucket.

4. **Bounded Action Plan & Governance:** Leadership requires a 30% reduction, but only 15% is recoverable this quarter without application-logic changes. Identify immediate engineering fixes, explain their performance/capacity trade-offs, and separate short-term cost controls from longer-term root-cause fixes. Finish with governance measures such as release cost gates and unit-cost dashboards.

Do not use a generic cost-cutting checklist. Focus on engineering evidence, correlation, root cause, and measurable unit-cost impact.








# Cost Scenario Analysis (Task 5)

## 1. Baseline

I would start with the CUR and Cost Explorer, normalized to a unit that reflects the app’s work: per 1M writes to DynamoDB, per GB-month for S3, per instance-hour for EKS, and per ELB-hour / LCU for ELB. The first question is not “what was the total bill” but “what changed in cost per unit of workload?”

For this scenario, a 5% business-volume increase while costs rose ~30% implies a unit-cost increase, not just more usage. I would compare this month vs prior month and vs the same month last quarter, then break each service into `cost / usage unit` and `cost / total bill`. If DynamoDB per write spike, S3 per GB-month spike, and ELB per request spike all move together, that is a production change or feature-level regression. If only EKS and ELB rose while business volume is flat, it suggests provisioning and retention drift rather than business growth.

## 2. Attribution approach (observe → hypothesize → verify)

One chain I would test: start with the CUR by service and usage-type dimensions (`DynamoDB`, `S3`, `EKS`, `ELB`) and compare `unblended` cost, actual bytes/reads/writes, and resource tags. Then:

- Observe: cost grew in all four services; usage growth was only ~5%.
- Hypothesis: the release increased write amplification and/or a feature started writing to the hot `user_event` path and S3 staging more aggressively; EKS and ELB are being driven by the same cause because they are downstream of the same app revision.
- Verify: pull CloudWatch metrics for DynamoDB write units, table storage, S3 object count and storage class transitions, EKS node CPU/memory and autoscaler churn, and ELB active connections / LCU. Then look for a step-change in the days surrounding the release and compare with a before/after ratio (cost per million writes, cost per GB stored, cost per 1000 requests).

For the ~40% untagged resources, I would attribute from resource-level evidence: account/region pairs, resource IDs, and service-level CloudWatch tags; for shared infrastructure I would use a `tag coverage` model and allocate by usage to the top app or table, while flagging the unknown remainder separately rather than pretending it is fully known.

Confounders: 31-day vs 30-day billing, RI/SP amortization, and one-off charges. I would remove them by comparing net and unblended cost, normalizing to daily rate, filtering by `usage` vs `discount` vs `one-time`, and checking invoice line items for snapshots, data transfers, and support-like one-time charges.

## 3. Action and trade-off

Constraint: the leader wants a 30% reduction, but only ~15% of the increase is realistically recoverable this quarter.

Actionable first step: fix the root cause rather than broad churn. Specifically, identify the app release’s biggest write-amplification change, set a write-budget guardrail per table, and reduce EKS overprovisioning / ELB over-scaling by tuning autoscaling and S3 lifecycle/retention. This is a “point fix” for the actual driver, while keeping the cost-safe baseline in place.

Fallback: if app-team capacity is too limited to change logic, stop incremental waste quickly: trim expensive storage classes, reduce idle EKS capacity, and explicitly hold off on new writes or retention until the change is ready. This gives up some future scale, but it avoids an uncontrolled bill.

The key is to distinguish a one-time optimization (e.g., deleting stale snapshots, shrinking idle clusters, changing lifecycle rules) from root-cause governance (tagging, per-service unit-cost dashboards, a release gate for write amplification, and cost owners for each service). That prevents regression by making the cost signal visible before the next release.

## 4. Conclusion

The strongest evidence is not the total bill alone; it is the combination of `cost-per-unit` trends across DynamoDB, S3, EKS, and ELB, normalized for billing days and adjusted to remove RI/SP and one-time charges. If that trend shows a sustained spike in cost per write or per request after the app revision, it is not a “billing anomaly”; it is a usage-efficiency change. The fix is to control the source, then add governance so the next release cannot silently raise the unit cost again.

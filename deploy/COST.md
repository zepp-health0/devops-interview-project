# Cost Scenario Analysis (Task 5)

## 1. Establish the Baseline

**Normalise first.** $40k/31d = $1,290/day; $52k/30d = $1,733/day — **+34.4%**, not +30%. The
shorter month *hides* severity: "fewer billing days" cannot absorb any of the rise, it widens it.

**Fix the lens.** Re-run in **amortized**. A quarter-start RI/SP purchase distorts unblended
twice: All-Upfront lands wholly in the purchase month, and commitments shift spend between line
item types (`SavingsPlanCoveredUsage`, `DiscountedUsage`, `RIFee`), changing *per-service
attribution* at flat usage.

**Pick the unit.** Total cost answers nothing. I track **$ per 1M `user_event` writes**, $ per 1k
requests, $ per GB-month. Volume +5% against a bill +30% puts unit cost up ~**25%**: an
efficiency regression, not growth.

## 2. Attribution (Observe → Hypothesise → Verify)

**Observe:** the rise spans four services, is partly decoupled from volume, and starts at the
revision.

**Hypothesise:** one root cause — requests became **smaller and more frequent**. Same bytes, far
more operations; everything downstream bills per *operation*, not per byte.

**Verify** — CUR by `line_item_usage_type`, daily, either side of the release:

| Service | Predicted signature | Confirming data |
|---|---|---|
| DynamoDB | WCU billed per 1 KB **rounded up**: writes outpace bytes; GSIs multiply them | `ConsumedWriteCapacityUnits` ÷ PutItem `SampleCount` |
| S3 | object *count* drives Tier-1 PUTs; storage is a **stock**, growing at flat write rate | Storage Lens count vs `BucketSizeBytes`; `Requests-Tier1` vs `TimedStorage-ByteHrs` |
| EMR | small files → more splits → longer clusters | cluster-hours vs input file count |
| ELB | LCU bills the **max of four dimensions**; without keep-alive, new connections dominate | `ConsumedLCUs` vs `NewConnectionCount` vs `ProcessedBytes` |
| EKS | more requests → more pods and cross-AZ traffic | split cost allocation data |

All five stepping at the release timestamp confirms one root cause. S3 storage climbing
*smoothly* while requests step means two problems: accumulation and the revision.

**Untagged 40%:** don't wait for tags. Join CUR `line_item_resource_id` to an AWS Config
aggregator or the Resource Groups Tagging API; EKS split cost allocation for pod-level, Storage
Lens for S3 prefixes. DynamoDB needs no tags — resource IDs are per-table.

**Confounders ruled out:** billing days, RI/SP amortization, one-off `Fee`/`Tax` and
data-transfer lines, region shifts, free-tier expiry, and a migration **backfill** — which
decides whether next month falls unaided.

## 3. Actions and Trade-offs

**Constraint: the leader wants −30%; ~15% is genuinely recoverable.**

- **Self-correcting** — the backfill, confirmed by checking the S3 PUT spike is bounded to the
  release window. Booking it as a "saving" would be dishonest.
- **Point fixes I own now (~10–15%)** — S3 lifecycle to IA/Glacier with expiry; **small-file
  compaction**, fixing S3 request cost *and* EMR runtime at once; scope PITR; log retention.
- **Source reduction (largest lever, blocked)** — batching, keep-alive, payload shape. Needs the
  APP team; next quarter.

**Commitment trade-off:** a **1-year no-upfront Compute Savings Plan** sized to the *floor* of
EKS/EMR usage — deliberately not DynamoDB reserved capacity, since that table migrates next
quarter. I give up the deeper 3-year discount to avoid stranding a commitment on infrastructure
that is leaving.

**Point fix vs root-cause governance.** Lifecycle and compaction are point fixes; the next
revision recreates them. The root cause is a revision that changed write shape with **no cost
review**. Governance: unit cost in the release checklist and on a dashboard, CUR anomaly
detection, per-tag budget alerts, and tag policy enforcement to shrink that 40%.

**To the leader:** −15% with named owners and dates, not −30%.

## 4. Supporting Experience (Optional)

> Deliberately blank: this asks for a real before/after from my own experience, and the rest of
> this document is only worth reading if nothing in it is invented.

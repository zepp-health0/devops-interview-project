<!-- # Cost Scenario Analysis (Task 5)

> This is where you answer Task 5. Up to 1,000 words. If you can get real data, give numbers and the queries you would run. If you cannot, write down what you are assuming, how you would check it, and what would change if the assumption turned out to be wrong. Tie each conclusion to a specific piece of data and to how you would check it — a general list of cost-saving tips is not what we are looking for.

## 1. Where You Start

What do you look at first to decide whether +30% is even surprising? Which unit do you measure in — cost per event, per request, per active user — and why that one? How do you tell "we are simply doing more business" apart from "we are wasting money"?

## 2. Where the Money Went

Walk through one chain of *what you saw → what you think it is → the exact data that would prove it*, until the 30% is broken into named pieces. Say which data you would use at each step:

- which dimension you split by first — service, usage type, account, region, tag;
- the 40% of resources with no tags: how do you work out who they belong to?
- four services rising at once: one shared cause, or several separate ones, and how would you tell?
- the other explanations you have to rule out before you trust your own: the different number of days in the two months, how those Reserved Instance and Savings Plan payments land in the bill, one-off charges.

## 3. What You Would Do

Pick one of the three situations in the README. Give a first step you could start on Monday, a fallback if it does not work, and what you give up by choosing it.

Then separate the one-off fix from the thing that keeps the cost from creeping back up, and say how you would keep it from creeping back.

## 4. Something You Have Done Before (Optional)

## 4. Supporting Experience (Optional)

Describe one real cost optimization, including the measured before-and-after result and how you confirmed that your change caused the improvement rather than a coincidental change in business volume. -->


Baseline: normalise before attributing
The headline is +30% ($40k → $52k). That number is wrong in a way that matters.

Previous month: $40k / 31 days = $1,290/day. Current: $52k / 30 days = $1,733/day. Daily run-rate is +34.3%, not +30%. The calendar is hiding about four points, not explaining them. Anyone who opens with "some of this is billing days" has the sign backwards.

Unit cost is the working measure: $ per 1k user_event writes and $ per 1M API requests. Business volume grew 5%, so unit cost rose ≈ 1.343 / 1.05 = +27.9%. That splits the problem: ~5pp is growth, ~28pp is efficiency. Only the second part is recoverable.

Unblended also has to go. RIs/SPs bought at quarter start distort it; I re-run everything on amortized and net amortized, and exclude line_item_line_item_type IN ('Fee','RIFee','Credit','Tax','Refund') to strip one-offs.

Attribution: observe → hypothesise → verify
Observe. Four services rose together — DynamoDB, S3, EKS, ELB. Four independent regressions in one month is implausible; one upstream change with fan-out is not.

Hypothesise. The app revision increased writes per business event (write amplification). One extra user_event write propagates: DynamoDB WCU → stream/backup PUTs to S3 → more objects for EMR to scan → more consumer pods on EKS → more LCU through ELB. Usage-proportional cost from a non-proportional cause.

Verify. Group the CUR by line_item_usage_type + line_item_operation + line_item_resource_id, daily, spanning the deploy date:

DynamoDB: WriteCapacityUnit-Hrs / WriteRequestUnits vs TimedStorage-ByteHrs. Storage flat + writes up ⇒ amplification, not retention.
S3: Requests-Tier1 (PUT) count vs TimedStorage. A PUT-count jump with flat bytes means more, smaller objects — which also inflates EMR scan cost.
ELB: split LCUUsage by NewConnection / ActiveConnection / ProcessedBytes. NewConnection-led ⇒ connection churn, not payload growth.
CloudWatch: ConsumedWriteCapacityUnits ÷ business events, before vs after deploy. The decisive test — if that ratio jumped ~28% while volume rose 5%, the hypothesis holds.
The untagged 40%. Tags are not required for this. line_item_resource_id gives table and bucket identity natively; EKS split cost allocation data attributes pod cost by namespace without tags. Tags matter for team chargeback, not for finding this. I backfill ownership by joining resource IDs against AWS Config inventory.

Confounders ruled out: billing days (computed above), RI/SP amortization (amortized view), one-offs (line-item-type filter), region/price changes (unit price per usage type held constant).

Trade-off: leadership wants −30%, ~15% is recoverable
I commit to 15%, with a named breakdown, and say plainly where the rest went.

Point fixes, this month, no APP capacity needed (~15%): S3 lifecycle + Intelligent-Tiering and aborting incomplete multipart uploads; DynamoDB provisioned-with-autoscaling where the write curve is now predictable; EKS rightsizing/consolidation; ELB keep-alive tuning to cut NewConnection LCU.

What I give up: I will not buy DynamoDB reserved capacity, even though it is the fastest single win. The table migrates next quarter — a 1-year commitment against a 3-month workload is a guaranteed write-off. I accept a worse rate now to avoid locking in.

Root-cause governance, needs APP (~the other 13%): batch writes, dedupe duplicate events. Deferred to next quarter's capacity, tracked as a named debt item rather than silently dropped.

Preventing regression is the actual deliverable. Publish $/1k events as a CloudWatch metric, alert at +10% week-over-week, and review it per deploy. This month's failure was not overspending — it was a revision changing unit economics and nobody noticing for 30 days. A lifecycle policy is a point fix; a unit-cost signal tied to deploys is the fix that stops the next one.
One cost cut you actually made: the numbers before and after, and how you convinced yourself the saving came from your change rather than from business volume moving on its own.

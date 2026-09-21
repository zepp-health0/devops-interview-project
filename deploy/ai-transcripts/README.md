# AI conversation records

Required by the assignment README: every prompt and every visible AI response,
one file per session, chronological, with only sensitive values redacted as
`[REDACTED: reason]`.

## Index

| # | File | Tool / model | Dates (UTC) | Content |
|---|------|--------------|-------------|---------|
| 1 | `01-claude-session-2026-09-14.md` | Claude (Anthropic), web interface — Claude Sonnet 4.6 | 2026-09-14 → 2026-09-20 | The full working session for Tasks 1–5: build failures, Dockerfile, compose and monitoring stack, CI/CD, the 3C investigation across six verify runs, NOTES.md and COST.md drafting. 72 exchanges. |
| 2 | `02-review-of-ai-output.md` | — | — | Cross-reference from NOTES.md §7: every piece of AI output that was rejected, changed, or corrected, with the exchange number where it happened. |

One session, one browser thread. Exchanges in that thread unrelated to this
assignment (an unrelated cover letter, an unrelated job application, a salary
question) are noted in brackets in the session file and omitted; nothing from
them was used here.

## What is verbatim and what is not

Prompts are reproduced as typed. Pasted tool output that ran to hundreds of
lines (loadgen status-code streams, full bucket distributions) is reproduced as
the lines that mattered, with the evidence file named so the full output can be
read there.

Responses are reproduced as written where they were prose. Where a response
consisted mainly of a generated file — the Dockerfile, `metrics.go`, the compose
stack, the CI workflow, NOTES.md revisions — the file is named by its
repository path rather than pasted inline: the committed file *is* that
response, and its later revisions are visible in git history.

The previously committed scaffold (`deploy/evidence/ai-transcript`, with
`«paste verbatim»` placeholders) has been removed. The reviewer's note of
2026-09-20 was correct that it was not a transcript.

## Redaction

```bash
grep -rniE 'ghp_|github_pat_|AKIA|BEGIN [A-Z ]*PRIVATE KEY' deploy/ai-transcripts/
```

Returns nothing. No credential was ever pasted into the session: the workflow
uses the run-scoped `GITHUB_TOKEN`, which is never echoed. Local filesystem
paths (`/home/sylva/devops-project`) appear in error output and are left as
they were — they are not sensitive.

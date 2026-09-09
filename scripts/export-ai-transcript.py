#!/usr/bin/env python3
"""Export Claude Code session logs to reviewable Markdown transcripts.

The README requires every prompt and every visible AI response to be committed under
deploy/ai-transcripts/. Claude Code already records each session as JSON Lines, so this converts
that authoritative log rather than reconstructing a summary from memory -- a hand-written recap
would be exactly the kind of record that cannot be audited.

Tool *calls* are included in full because the model produced them. Tool *results* are environment
output rather than AI output, and some are whole file dumps, so they are previewed and truncated.

    usage: scripts/export-ai-transcript.py [--source DIR] [--out DIR]
"""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import sys
from datetime import datetime

# Sensitive values are replaced, never deleted, so the surrounding record stays reviewable.
REDACTIONS: list[tuple[re.Pattern[str], str]] = [
    (re.compile(r"gh[pousr]_[A-Za-z0-9]{16,}"), "[REDACTED: GitHub token]"),
    (re.compile(r"github_pat_[A-Za-z0-9_]{20,}"), "[REDACTED: GitHub token]"),
    (re.compile(r"\bAKIA[0-9A-Z]{16}\b"), "[REDACTED: AWS access key id]"),
    (re.compile(r"\bASIA[0-9A-Z]{16}\b"), "[REDACTED: AWS temporary key id]"),
    (re.compile(r"(?i)\b(aws_secret_access_key|secret[_-]?key)\b\s*[:=]\s*\S+"),
     r"\1=[REDACTED: secret]"),
    (re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._\-]{20,}"), "Bearer [REDACTED: bearer token]"),
    (re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----", re.S),
     "[REDACTED: private key]"),
    (re.compile(r"\b[\w.+-]+@[\w-]+\.[\w.-]+\b"), "[REDACTED: email address]"),
]

TOOL_RESULT_PREVIEW = 800


def redact(text: str) -> str:
    for pattern, replacement in REDACTIONS:
        text = pattern.sub(replacement, text)
    return text


def fence(text: str, lang: str = "") -> str:
    """Wrap in a fence long enough to survive backticks inside the payload."""
    longest = max((len(m) for m in re.findall(r"`+", text)), default=0)
    bar = "`" * max(3, longest + 1)
    return f"{bar}{lang}\n{text.rstrip()}\n{bar}"


def stringify(value) -> str:
    if isinstance(value, str):
        return value
    return json.dumps(value, indent=2, ensure_ascii=False, default=str)


def render_block(block, out: list[str]) -> None:
    if isinstance(block, str):
        out.append(redact(block))
        return
    if not isinstance(block, dict):
        out.append(fence(stringify(block), "json"))
        return

    kind = block.get("type")

    if kind == "text":
        out.append(redact(block.get("text", "")))

    elif kind == "thinking":
        body = redact(block.get("thinking", "")).strip()
        if body:
            out.append("<details><summary>Reasoning</summary>\n\n" + fence(body) + "\n\n</details>")

    elif kind == "tool_use":
        out.append(f"**Tool call — `{block.get('name', '?')}`**")
        out.append(fence(redact(stringify(block.get("input", {}))), "json"))

    elif kind == "tool_result":
        body = redact(stringify(block.get("content", ""))).strip()
        truncated = len(body) > TOOL_RESULT_PREVIEW
        shown = body[:TOOL_RESULT_PREVIEW] + ("\n… [truncated]" if truncated else "")
        label = "Tool result (truncated)" if truncated else "Tool result"
        out.append(f"<details><summary>{label}</summary>\n\n" + fence(shown) + "\n\n</details>")

    elif kind == "image":
        out.append("_[image omitted]_")

    else:
        out.append(fence(redact(stringify(block)), "json"))


def render_entry(entry: dict) -> str | None:
    role = entry.get("type")
    if role not in ("user", "assistant"):
        return None

    message = entry.get("message") or {}
    content = message.get("content")
    if content is None:
        return None

    blocks: list[str] = []
    if isinstance(content, list):
        for block in content:
            render_block(block, blocks)
    else:
        render_block(content, blocks)

    body = "\n\n".join(b for b in blocks if b and b.strip())
    if not body.strip():
        return None

    stamp = entry.get("timestamp", "")
    if stamp:
        try:
            stamp = datetime.fromisoformat(stamp.replace("Z", "+00:00")).strftime("%Y-%m-%d %H:%M:%SZ")
        except ValueError:
            pass

    speaker = "User" if role == "user" else "Assistant"
    return f"### {speaker} — {stamp}\n\n{body}"


def export(path: pathlib.Path, out_dir: pathlib.Path) -> pathlib.Path | None:
    entries = []
    for line in path.read_text(errors="replace").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            entries.append(json.loads(line))
        except json.JSONDecodeError:
            continue

    if not entries:
        return None

    sections = [s for s in (render_entry(e) for e in entries) if s]
    if not sections:
        return None

    def first_value(key: str, default: str = "unknown") -> str:
        # Fields are not all present on every line, so each is looked up independently rather
        # than taken from whichever entry happens to carry the session id.
        return next((e[key] for e in entries if e.get(key)), default)

    meta = {k: first_value(k) for k in ("sessionId", "cwd", "gitBranch")}
    first = next((e.get("timestamp") for e in entries if e.get("timestamp")), "unknown")
    last = next((e.get("timestamp") for e in reversed(entries) if e.get("timestamp")), "unknown")
    models = sorted({
        (e.get("message") or {}).get("model")
        for e in entries
        if (e.get("message") or {}).get("model")
    })

    header = "\n".join([
        f"# Claude Code session — {path.stem}",
        "",
        "| | |",
        "|---|---|",
        "| Tool | Claude Code (CLI) |",
        f"| Model(s) | {', '.join(models) if models else 'unknown'} |",
        f"| Session id | `{meta['sessionId']}` |",
        f"| Working directory | `{meta['cwd']}` |",
        f"| Git branch | `{meta['gitBranch']}` |",
        f"| Started | {first} |",
        f"| Ended | {last} |",
        f"| Exchanges | {len(sections)} |",
        "",
        "Exported verbatim from the Claude Code session log by `scripts/export-ai-transcript.py`.",
        "Chronological order. Sensitive values are replaced with `[REDACTED: reason]`; tool results",
        f"are previewed to {TOOL_RESULT_PREVIEW} characters because they are environment output, not",
        "model output.",
        "",
        "---",
        "",
    ])

    out_dir.mkdir(parents=True, exist_ok=True)
    stamp = (first or "")[:10] or "undated"
    target = out_dir / f"{stamp}-claude-code-{path.stem[:8]}.md"
    target.write_text(header + "\n\n---\n\n".join(sections) + "\n")
    return target


def main() -> int:
    default_source = (
        pathlib.Path.home() / ".claude" / "projects"
        / "-Users-byroncustodio-WebstormProjects-devops-interview-project"
    )
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=pathlib.Path, default=default_source)
    parser.add_argument("--out", type=pathlib.Path,
                        default=pathlib.Path(__file__).resolve().parent.parent / "deploy" / "ai-transcripts")
    args = parser.parse_args()

    if not args.source.is_dir():
        print(f"no session directory at {args.source}", file=sys.stderr)
        return 1

    logs = sorted(args.source.glob("*.jsonl"), key=lambda p: p.stat().st_mtime)
    if not logs:
        print(f"no .jsonl session logs in {args.source}", file=sys.stderr)
        return 1

    for log in logs:
        written = export(log, args.out)
        if written:
            print(f"{written.relative_to(pathlib.Path.cwd())}  ({written.stat().st_size:,} bytes)")
        else:
            print(f"skipped empty session {log.name}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

#!/usr/bin/env python3
"""Measure a Docker build context after applying .dockerignore.

A multi-gigabyte context is what put ec-prod on the floor: data/backups was
4.3 GB, .dockerignore did not exclude it, and every `docker compose build`
shipped and snapshotted the whole tree. This is the regression gate, so it runs
without a Docker daemon.

Usage:
  build_context_size.py [context_dir] [--max-mb N] [--json] [--top N]

Exits 1 when the context exceeds --max-mb.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from pathlib import Path


def compile_pattern(pattern: str) -> re.Pattern[str]:
    """Translate one .dockerignore pattern to a regex.

    Follows moby/patternmatcher: `*` does not cross a separator, `**` does,
    `?` matches one non-separator character.
    """
    parts = pattern.strip("/").split("/")
    out: list[str] = []
    for i, part in enumerate(parts):
        if part == "**":
            # `**` swallows this and any following segments.
            out.append("(?:.*)" if i else "(?:.*)")
            continue
        seg = ""
        for ch in part:
            if ch == "*":
                seg += "[^/]*"
            elif ch == "?":
                seg += "[^/]"
            else:
                seg += re.escape(ch)
        out.append(seg)
    # Join, collapsing the separator that follows a `**`.
    joined = ""
    for i, seg in enumerate(out):
        if i:
            joined += "/" if not out[i - 1].startswith("(?:.*)") else "/?"
        joined += seg
    return re.compile("^" + joined + "$")


def load_rules(context: Path) -> list[tuple[re.Pattern[str], bool]]:
    ignore = context / ".dockerignore"
    rules: list[tuple[re.Pattern[str], bool]] = []
    if not ignore.is_file():
        return rules
    for raw in ignore.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        negated = line.startswith("!")
        if negated:
            line = line[1:].strip()
        if not line:
            continue
        rules.append((compile_pattern(line), negated))
    return rules


def excluded(rel: str, rules: list[tuple[re.Pattern[str], bool]]) -> bool:
    """Last matching rule wins; a matching ancestor excludes the whole subtree."""
    verdict = False
    candidates = [rel]
    parent = os.path.dirname(rel)
    while parent:
        candidates.append(parent)
        parent = os.path.dirname(parent)
    for pattern, negated in rules:
        if negated:
            if pattern.match(rel):
                verdict = False
        elif any(pattern.match(c) for c in candidates):
            verdict = True
    return verdict


def measure(context: Path) -> tuple[int, int, dict[str, int]]:
    rules = load_rules(context)
    total = 0
    files = 0
    by_top: dict[str, int] = {}
    for dirpath, dirnames, filenames in os.walk(context, followlinks=False):
        rel_dir = os.path.relpath(dirpath, context)
        rel_dir = "" if rel_dir == "." else rel_dir
        # Prune excluded directories so a 4 GB tree is never walked.
        kept = []
        for d in dirnames:
            rel = os.path.join(rel_dir, d) if rel_dir else d
            if not excluded(rel, rules):
                kept.append(d)
        dirnames[:] = kept
        for f in filenames:
            rel = os.path.join(rel_dir, f) if rel_dir else f
            if excluded(rel, rules):
                continue
            try:
                size = os.lstat(os.path.join(dirpath, f)).st_size
            except OSError:
                continue
            total += size
            files += 1
            top = rel.split("/", 1)[0] if "/" in rel else rel
            by_top[top] = by_top.get(top, 0) + size
    return total, files, by_top


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("context", nargs="?", default=".")
    ap.add_argument("--max-mb", type=float, default=0)
    ap.add_argument("--top", type=int, default=10)
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()

    context = Path(args.context).resolve()
    total, files, by_top = measure(context)
    mb = total / (1024 * 1024)

    if args.json:
        print(json.dumps({"bytes": total, "mb": round(mb, 2), "files": files}))
    else:
        print(f"context   : {context}")
        print(f"size      : {mb:.1f} MB ({total} bytes) across {files} files")
        for name, size in sorted(by_top.items(), key=lambda kv: -kv[1])[: args.top]:
            print(f"  {size / (1024 * 1024):8.1f} MB  {name}")

    if args.max_mb and mb > args.max_mb:
        print(
            f"FAIL: build context {mb:.1f} MB exceeds the {args.max_mb} MB budget.\n"
            "Add the offending tree to .dockerignore; a multi-gigabyte context "
            "fills the production root filesystem one build at a time.",
            file=sys.stderr,
        )
        return 1
    if args.max_mb:
        print(f"OK: within the {args.max_mb} MB budget")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

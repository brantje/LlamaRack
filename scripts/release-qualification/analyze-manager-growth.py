#!/usr/bin/env python3
import csv
import json
import statistics
import sys
from pathlib import Path

if len(sys.argv) != 3:
    raise SystemExit("usage: analyze-manager-growth.py <samples.tsv> <summary.json>")

sample_path = Path(sys.argv[1])
summary_path = Path(sys.argv[2])
with sample_path.open(encoding="utf-8") as handle:
    rows = [row for row in csv.DictReader(handle, delimiter="\t") if row.get("goroutines") and row.get("rss_kib")]

if len(rows) < 4:
    raise SystemExit(f"not enough manager resource samples: {len(rows)}")

window = min(3, len(rows) // 2)
first = rows[:window]
last = rows[-window:]
initial_goroutines = statistics.median(int(row["goroutines"]) for row in first)
final_goroutines = statistics.median(int(row["goroutines"]) for row in last)
initial_rss_kib = statistics.median(int(row["rss_kib"]) for row in first)
final_rss_kib = statistics.median(int(row["rss_kib"]) for row in last)

goroutine_limit = max(initial_goroutines + 16, initial_goroutines * 1.5)
rss_limit_kib = max(initial_rss_kib + 256 * 1024, initial_rss_kib * 1.75)

summary = {
    "samples": len(rows),
    "initial_goroutines": initial_goroutines,
    "final_goroutines": final_goroutines,
    "goroutine_limit": goroutine_limit,
    "initial_manager_rss_kib": initial_rss_kib,
    "final_manager_rss_kib": final_rss_kib,
    "manager_rss_limit_kib": rss_limit_kib,
}
summary_path.parent.mkdir(parents=True, exist_ok=True)
summary_path.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")

failures = []
if final_goroutines > goroutine_limit:
    failures.append(
        f"manager goroutines grew from {initial_goroutines} to {final_goroutines} (limit {goroutine_limit:.0f})"
    )
if final_rss_kib > rss_limit_kib:
    failures.append(
        f"manager RSS grew from {initial_rss_kib} KiB to {final_rss_kib} KiB (limit {rss_limit_kib:.0f} KiB)"
    )
if failures:
    raise SystemExit("; ".join(failures))

print(
    f"manager growth bounded: goroutines {initial_goroutines}->{final_goroutines}, "
    f"RSS {initial_rss_kib}->{final_rss_kib} KiB"
)

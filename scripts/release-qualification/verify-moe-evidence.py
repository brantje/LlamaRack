#!/usr/bin/env python3
"""Validate the recorded llama.cpp worker arguments for CPU MoE offload."""

import sys
from pathlib import Path

if len(sys.argv) != 3:
    raise SystemExit("usage: verify-moe-evidence.py <worker-args.txt> <expected-n-cpu-moe>")

args = Path(sys.argv[1]).read_text(encoding="utf-8").splitlines()
expected = sys.argv[2]
try:
    index = args.index("--n-cpu-moe")
except ValueError as exc:
    raise SystemExit("MoE worker arguments do not contain --n-cpu-moe") from exc

if index + 1 >= len(args):
    raise SystemExit("--n-cpu-moe is missing its value")
if args[index + 1] != expected:
    raise SystemExit(f"--n-cpu-moe={args[index + 1]!r}, expected {expected!r}")

print(f"MoE CPU offload evidence verified: --n-cpu-moe {expected}")

#!/usr/bin/env python3
"""Validate recorded llama.cpp worker arguments for dense multi-GPU placement."""

import sys
from pathlib import Path

if len(sys.argv) != 2:
    raise SystemExit("usage: verify-dense-multi-evidence.py <worker-args.txt>")

args = Path(sys.argv[1]).read_text(encoding="utf-8").splitlines()
if args.count("--device") != 1:
    raise SystemExit("dense multi-GPU worker arguments must contain exactly one --device")
try:
    index = args.index("--device")
except ValueError as exc:
    raise SystemExit("dense multi-GPU worker arguments do not contain --device") from exc

if index + 1 >= len(args):
    raise SystemExit("--device is missing its value")
devices = args[index + 1]
if "," not in devices:
    raise SystemExit(f"--device={devices!r}, expected a comma-separated multi-GPU list")

if args.count("--tensor-split") != 1:
    raise SystemExit("dense multi-GPU worker arguments must contain exactly one --tensor-split")
try:
    split_index = args.index("--tensor-split")
except ValueError as exc:
    raise SystemExit("dense multi-GPU worker arguments do not contain --tensor-split") from exc
if split_index + 1 >= len(args) or not args[split_index + 1].strip():
    raise SystemExit("--tensor-split is missing its value")

print(f"dense multi-GPU placement evidence verified: --device {devices} --tensor-split {args[split_index + 1]}")

#!/usr/bin/env python3
"""
EPaxos log analyzer (folder-based, no per-instance rows)

Parses EPaxos replica logs and computes:
- Per-replica metrics: starts, fast/slow/unknown counts, conflict rate, shared-key fraction,
  slow-on-shared, slow-on-unique, and percentage rates.
- Cluster-wide metrics aggregated from per-replica metrics (same fields).

Outputs (in --outdir):
  - replica_metrics.csv
  - cluster_metrics.csv

Usage:
  python analyze_epaxos_logs.py \
      --logdir ./logs \
      --shared-key share_key \
      --outdir ./epaxos_log_metrics
"""

import argparse
import csv
import os
import re
from typing import Dict, Tuple

# ---------- CLI ----------
def parse_args():
    p = argparse.ArgumentParser(description="Analyze EPaxos replica logs from a folder.")
    p.add_argument("--logdir", default="./logs", help="Folder containing log files (default: current dir)")
    p.add_argument("--shared-key", default="share_key", help='Hot/shared key string (default: "share_key")')
    p.add_argument("--outdir", default="./epaxos_log_metrics", help="Output directory for CSVs")
    return p.parse_args()

# ---------- Regex patterns ----------
RE_REPLICA_ID   = re.compile(r'"replica_id"\s*:\s*(\d+)')
RE_INSTANCE_ID  = re.compile(r'"instance_id"\s*:\s*(\d+)')

RE_PROPOSAL_START = re.compile(r'"tags"\s*:\s*\[[^\]]*"proposal"[^\]]*"start"[^\]]*"consensus"[^\]]*\]')
RE_FAST_PATH      = re.compile(r'Fast path consensus achieved|"\s*fast_path\s*"')
RE_SLOW_FALLBACK  = re.compile(r'Falling back to slow path')
RE_SLOW_DONE      = re.compile(r'Slow path consensus completed|"\s*slow_path\s*"\s*,\s*"\s*completed\s*"')

RE_CONFLICT_TAG   = re.compile(r'"tags"\s*:\s*\[[^\]]*"conflict"[^\]]*"dependency"[^\]]*\]')
RE_CONFLICTING_INSTANCE = re.compile(r'"conflicting_instance"\s*:\s*"R(\d+)\.(\d+)"')

RE_KEY_IN_COMMAND = re.compile(r'"command"\s*:\s*\{[^}]*?"Key"\s*:\s*"([^"]+)"')
RE_KEY_FLAT       = re.compile(r'"Key"\s*:\s*"([^"]+)"')

def classify_key(key: str, shared_key: str) -> str:
    if not key:
        return "unknown"
    return "shared" if key == shared_key else "unique"

class InstanceState:
    __slots__ = ("replica_id","instance_id","key","started","saw_fast","saw_slow",
                 "conflicted","conflict_edges")
    def __init__(self, rid:int, iid:int):
        self.replica_id = rid
        self.instance_id = iid
        self.key = None
        self.started = False
        self.saw_fast = False
        self.saw_slow = False
        self.conflicted = False
        self.conflict_edges = 0

    def outcome(self):
        # If both appear, treat as slow (fallback overrides early fast signal)
        if self.saw_slow:
            return "slow"
        if self.saw_fast:
            return "fast"
        return "unknown"

def percent(numer: int, denom: int) -> float:
    return (100.0 * numer / denom) if denom else 0.0

def parse_log_file(path: str, shared_key: str):
    """Parses a single log file and returns (replica_id, per_instance_map)."""
    per_instance: Dict[Tuple[int,int], InstanceState] = {}
    rid_from_file = None

    def get_state(rid, iid):
        k = (rid, iid)
        st = per_instance.get(k)
        if st is None:
            st = InstanceState(rid, iid)
            per_instance[k] = st
        return st

    with open(path, "r", encoding="utf-8", errors="ignore") as f:
        for line in f:
            if '"replica_id"' not in line or '"instance_id"' not in line:
                continue
            rid_m = RE_REPLICA_ID.search(line)
            iid_m = RE_INSTANCE_ID.search(line)
            if not rid_m or not iid_m:
                continue
            rid = int(rid_m.group(1))
            iid = int(iid_m.group(1))
            if rid_from_file is None:
                rid_from_file = rid

            st = get_state(rid, iid)

            if RE_PROPOSAL_START.search(line):
                st.started = True
                m = RE_KEY_IN_COMMAND.search(line) or RE_KEY_FLAT.search(line)
                if m:
                    st.key = m.group(1)

            if RE_FAST_PATH.search(line):
                st.saw_fast = True

            if RE_SLOW_FALLBACK.search(line) or RE_SLOW_DONE.search(line):
                st.saw_slow = True

            if RE_CONFLICT_TAG.search(line):
                st.conflicted = True
                if RE_CONFLICTING_INSTANCE.search(line):
                    st.conflict_edges += 1

    # Keep only instances started on this replica
    per_instance = {k:v for k,v in per_instance.items() if v.started}
    return (rid_from_file if rid_from_file is not None else -1), per_instance

def aggregate_replica_metrics(per_instance_map: Dict[Tuple[int,int], InstanceState], shared_key: str, replica_id: int):
    started = list(per_instance_map.values())
    starts = len(started)

    fast = sum(1 for st in started if st.outcome() == "fast")
    slow = sum(1 for st in started if st.outcome() == "slow")
    unknown = starts - fast - slow

    shared = [st for st in started if classify_key(st.key, shared_key) == "shared"]
    unique = [st for st in started if classify_key(st.key, shared_key) == "unique"]
    unknown_key = starts - len(shared) - len(unique)

    slow_on_shared = sum(1 for st in shared if st.outcome() == "slow")
    slow_on_unique = sum(1 for st in unique if st.outcome() == "slow")

    conflicted = sum(1 for st in started if st.conflicted)
    conflict_edges = sum(st.conflict_edges for st in started)

    return {
        "replica_id": replica_id,
        "starts": starts,
        "fast": fast,
        "slow": slow,
        "unknown_outcome": unknown,

        "fast_rate_pct": f"{percent(fast, starts):.2f}%",
        "slow_rate_pct": f"{percent(slow, starts):.2f}%",

        "shared_starts": len(shared),
        "unique_starts": len(unique),
        "unknown_key_starts": unknown_key,
        "shared_key_fraction_pct": f"{percent(len(shared), starts):.2f}%",

        "slow_on_shared": slow_on_shared,
        "slow_on_unique": slow_on_unique,
        "slow_rate_shared_only_pct": f"{percent(slow_on_shared, len(shared)):.2f}%" if shared else "0.00%",
        "slow_rate_unique_only_pct": f"{percent(slow_on_unique, len(unique)):.2f}%" if unique else "0.00%",

        "conflicted_instances": conflicted,
        "conflict_edges": conflict_edges,
        "conflict_rate_pct": f"{percent(conflicted, starts):.2f}%"
    }

def print_table(title, headers, rows):
    widths = [max(len(h), *(len(str(r.get(h, ""))) for r in rows)) for h in headers]
    print(f"\n{title}")
    print(" | ".join(h.ljust(w) for h, w in zip(headers, widths)))
    print("-+-".join("-"*w for w in widths))
    for r in rows:
        print(" | ".join(str(r.get(h, "")).ljust(w) for h, w in zip(headers, widths)))

def main():
    args = parse_args()
    logdir = os.path.abspath(args.logdir)
    outdir = os.path.abspath(args.outdir)
    os.makedirs(outdir, exist_ok=True)

    # Collect *.log files in the folder
    files = [os.path.join(logdir, fn) for fn in os.listdir(logdir) if fn.endswith(".log")]
    files.sort()
    if not files:
        raise SystemExit(f"No .log files found in folder: {logdir}")

    per_replica = []
    for path in files:
        rid, per_instance = parse_log_file(path, args.shared_key)
        # If a file contains no started instances, skip it but warn
        if rid is None or not per_instance:
            # Still try to infer replica id from filename like epaxos_replica_<id>.log
            m = re.search(r'(\d+)', os.path.basename(path))
            rid = int(m.group(1)) if m else -1
        metrics = aggregate_replica_metrics(per_instance, args.shared_key, rid)
        metrics["log_file"] = os.path.basename(path)
        per_replica.append(metrics)

    # ----- Print per-replica table -----
    r_headers = [
        "replica_id","log_file","starts","fast","slow","unknown_outcome",
        "fast_rate_pct","slow_rate_pct",
        "shared_starts","unique_starts","unknown_key_starts","shared_key_fraction_pct",
        "slow_on_shared","slow_on_unique","slow_rate_shared_only_pct","slow_rate_unique_only_pct",
        "conflicted_instances","conflict_edges","conflict_rate_pct"
    ]
    per_replica_sorted = sorted(per_replica, key=lambda m: m["replica_id"])
    print_table("Per-replica EPaxos metrics", r_headers, per_replica_sorted)

    # ----- Cluster-wide aggregation from per-replica totals -----
    c_starts = sum(int(m["starts"]) for m in per_replica_sorted)
    c_fast   = sum(int(m["fast"]) for m in per_replica_sorted)
    c_slow   = sum(int(m["slow"]) for m in per_replica_sorted)
    c_unknown= sum(int(m["unknown_outcome"]) for m in per_replica_sorted)

    c_shared = sum(int(m["shared_starts"]) for m in per_replica_sorted)
    c_unique = sum(int(m["unique_starts"]) for m in per_replica_sorted)
    c_unknown_key = sum(int(m["unknown_key_starts"]) for m in per_replica_sorted)

    c_slow_shared = sum(int(m["slow_on_shared"]) for m in per_replica_sorted)
    c_slow_unique = sum(int(m["slow_on_unique"]) for m in per_replica_sorted)

    c_conflicted = sum(int(m["conflicted_instances"]) for m in per_replica_sorted)
    c_conflict_edges = sum(int(m["conflict_edges"]) for m in per_replica_sorted)

    cluster_row = {
        "starts": c_starts,
        "fast": c_fast,
        "slow": c_slow,
        "unknown_outcome": c_unknown,

        "fast_rate_pct": f"{percent(c_fast, c_starts):.2f}%",
        "slow_rate_pct": f"{percent(c_slow, c_starts):.2f}%",

        "shared_starts": c_shared,
        "unique_starts": c_unique,
        "unknown_key_starts": c_unknown_key,
        "shared_key_fraction_pct": f"{percent(c_shared, c_starts):.2f}%",

        "slow_on_shared": c_slow_shared,
        "slow_on_unique": c_slow_unique,
        "slow_rate_shared_only_pct": f"{percent(c_slow_shared, c_shared):.2f}%" if c_shared else "0.00%",
        "slow_rate_unique_only_pct": f"{percent(c_slow_unique, c_unique):.2f}%" if c_unique else "0.00%",

        "conflicted_instances": c_conflicted,
        "conflict_edges": c_conflict_edges,
        "conflict_rate_pct": f"{percent(c_conflicted, c_starts):.2f}%"
    }

    # ----- Print cluster-wide table -----
    c_headers = list(cluster_row.keys())
    print_table("Cluster-wide EPaxos metrics", c_headers, [cluster_row])

    # ----- Write CSVs -----
    # Per-replica metrics
    replica_csv = os.path.join(outdir, "replica_metrics.csv")
    with open(replica_csv, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=r_headers + ["log_file"])  # ensure order
        w.writeheader()
        for m in per_replica_sorted:
            w.writerow(m)

    # Cluster metrics
    cluster_csv = os.path.join(outdir, "cluster_metrics.csv")
    with open(cluster_csv, "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=c_headers)
        w.writeheader()
        w.writerow(cluster_row)

    print(f"\nSaved CSVs to: {outdir}")
    print(f"- {replica_csv}\n- {cluster_csv}")

if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Run CzzEdit's legacy detector on an events file and print the result as JSON
in MomentForge's key format, so Go output can be compared field by field.

Usage:
  LEGACY=$(mktemp -d) && git archive legacy-python czzedit | tar -x -C "$LEGACY"
  python3 -I legacy_detect.py --legacy "$LEGACY" --events case.events.json --end 600000 \
      [--config '{"sensitive": true}'] [--gaps '[[600000, 640000]]'] [--out result.json]

Events file: JSON list of {"id", "seq", "offsetMs", "authorKey" (null allowed), "text"}.
Output: {"candidates": [...], "windows": [...]} with signal keys mapped
D1→laugh, D2→surge, D3:x→repeat:x, D4:x→emotion:x, D5→participants.
Standard library only; works on Python 3.9+.
"""
import argparse
import json
import sys
from pathlib import Path

KEY_MAP = {"D1": "laugh", "D2": "surge", "D5": "participants"}
PREFIX_MAP = {"D3": "repeat", "D4": "emotion"}


def key(k):
    if k in KEY_MAP:
        return KEY_MAP[k]
    head, _, rest = k.partition(":")
    if head in PREFIX_MAP:
        return PREFIX_MAP[head] + ":" + rest
    raise SystemExit(f"unknown legacy signal key: {k!r}")


def ratios(d):
    return {key(k): v for k, v in (d or {}).items()}


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--legacy", required=True, help="folder containing the extracted czzedit/ package")
    ap.add_argument("--events", required=True)
    ap.add_argument("--end", type=int, required=True, help="analysis end in ms")
    ap.add_argument("--config", default="{}", help="JSON of CzzEdit Config fields")
    ap.add_argument("--gaps", default="[]", help="JSON list of [start_ms, end_ms]")
    ap.add_argument("--out")
    args = ap.parse_args()

    sys.path.insert(0, str(Path(args.legacy).resolve()))
    from czzedit.common import Config  # noqa: E402
    from czzedit.detector import analyze  # noqa: E402

    raw = json.loads(Path(args.events).read_text(encoding="utf-8"))
    events = [{"event_id": e["id"], "seq": e["seq"], "time_ms": e["offsetMs"],
               "author_key": e["authorKey"], "content": e["text"]} for e in raw]
    events.sort(key=lambda e: (e["time_ms"], e["seq"]))
    gaps = [tuple(g) for g in json.loads(args.gaps)]
    result = analyze(events, args.end, Config(**json.loads(args.config)), gaps)

    candidates = [{
        "id": m["id"], "groupId": m["group_id"],
        "reactionStartMs": m["reaction_start_ms"], "reactionEndMs": m["reaction_end_ms"],
        "peakMs": m["peak_ms"], "clipStartMs": m["clip_start_ms"], "clipEndMs": m["clip_end_ms"],
        "signals": sorted(key(s) for s in m["signals"]), "reasons": m["reasons"],
        "peak": {"n": m["peak_stats"]["n"], "u": m["peak_stats"]["u"], "ratio": m["peak_stats"]["ratio"],
                 "z": m["peak_stats"]["z"], "baselineN": m["peak_stats"]["baseline_n"],
                 "reactionRatios": ratios(m["peak_stats"]["reaction_ratios"])},
        "baselineReady": m["baseline_ready"], "evidenceIds": m["evidence_ids"],
        "partialContext": m["partial_context"],
    } for m in result["markers"]]
    windows = [{
        "t": w["t"], "n": w["n"], "u": w["u"], "ratio": w["ratio"], "z": w["z"],
        "baselineN": w["baseline_n"], "baselineReady": w["baseline_ready"], "valid": w["valid"],
        "signals": sorted(key(s) for s in w["signals"]), "reactionRatios": ratios(w["reaction_ratios"]),
    } for w in result["windows"]]
    text = json.dumps({"candidates": candidates, "windows": windows}, ensure_ascii=False, indent=1) + "\n"
    if args.out:
        Path(args.out).write_text(text, encoding="utf-8")
    else:
        sys.stdout.write(text)


if __name__ == "__main__":
    main()

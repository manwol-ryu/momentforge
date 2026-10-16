#!/usr/bin/env python3
"""Regenerates the LIVE comparison scripts used by TestLiveMatchesLegacy.

  LEGACY=$(mktemp -d) && git archive legacy-python czzedit | tar -x -C "$LEGACY"
  python3 -I internal/detect/rules/testdata/legacy/gen_live.py --legacy "$LEGACY"

A synthetic 55-minute broadcast is replayed into CzzEdit's LiveAnalyzer the
way live_service drives it: chat arrives up to 5 s ahead of the watermark,
a 1-minute disconnect becomes a gap, some messages arrive late, viewer
samples come every minute and drop 16 % near minute 44. Every operation and
every result (released candidates, periodic previews) is written as
<name>.steps.json for the Go test to replay. All chat is synthetic.
"""
import argparse
import json
import random
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
KEY_MAP = {"D1": "laugh", "D2": "surge", "D5": "participants"}
PREFIX_MAP = {"D3": "repeat", "D4": "emotion"}


def key(k):
    if k in KEY_MAP:
        return KEY_MAP[k]
    head, _, rest = k.partition(":")
    return PREFIX_MAP[head] + ":" + rest


def viewer(v):
    if v is None:
        return None
    return {"mode": v["mode"], "epochStartMs": v["epoch_start_ms"], "reference": v["reference"],
            "average": v["average"], "status": v["status"]}


def cand(m, state=None):
    out = {
        "id": m["id"], "groupId": m["group_id"], "reactionStartMs": m["reaction_start_ms"],
        "reactionEndMs": m["reaction_end_ms"], "peakMs": m["peak_ms"], "clipStartMs": m["clip_start_ms"],
        "clipEndMs": m["clip_end_ms"], "signals": sorted(key(s) for s in m["signals"]), "reasons": m["reasons"],
        "peak": {"n": m["peak_stats"]["n"], "u": m["peak_stats"]["u"], "ratio": m["peak_stats"]["ratio"],
                 "z": m["peak_stats"]["z"], "baselineN": m["peak_stats"]["baseline_n"],
                 "reactionRatios": {key(k): v for k, v in (m["peak_stats"]["reaction_ratios"] or {}).items()}},
        "baselineReady": m["baseline_ready"], "evidenceIds": m["evidence_ids"],
        "partialContext": m["partial_context"],
        "state": state or m.get("state") or ("partial" if m["partial_context"] else "confirmed"),
        "viewerBaseline": viewer(m.get("viewer_baseline")),
    }
    if m.get("baseline_transition"):  # Go omits the field when false
        out["baselineTransition"] = True
    return out


def scenario():
    rnd = random.Random(42)
    authors = [f"a{i:02d}" for i in range(40)]
    casual = ["오늘 방송 재밌다", "GG", "이거 뭐임", "ㅇㅇ", "굿굿", "lol", "오 좋다", "화이팅", "음악 좋네"]
    end = 3_300_000
    rows = []
    for block in range(0, end, 10_000):
        for _ in range(rnd.randint(8, 14)):
            rows.append((block + rnd.randrange(10_000), rnd.choice(authors), rnd.choice(casual)))

    def burst(start, length, n, texts):
        for i in range(n):
            t = start + rnd.randrange(3000)
            while t < start + length:
                rows.append((t, f"r{i:02d}", rnd.choice(texts)))
                t += rnd.randint(2000, 5000)

    burst(600_000, 20_000, 20, ["ㅋㅋㅋㅋ", "ㅋㅋ", "ㅎㅎㅎ"])
    burst(1_300_000, 20_000, 18, ["클립 각", "클립 각!"])
    burst(2_000_000, 20_000, 18, ["와", "헐", "ㄷㄷㄷ"])
    burst(2_850_000, 200_000, 24, ["ㅋㅋㅋㅋ", "ㅋㅋㅋㅋㅋ"])  # long: split, crosses the viewer drop
    burst(3_150_000, 20_000, 18, ["ㅠㅠㅠ", "아깝다"])
    gap = (1_500_000, 1_560_000)
    rows = [r for r in rows if not gap[0] <= r[0] < gap[1]]
    rows.sort(key=lambda r: r[0])
    events = [{"event_id": f"l{i + 1:05d}", "seq": i + 1, "time_ms": t, "author_key": a, "content": c}
              for i, (t, a, c) in enumerate(rows)]
    samples = []
    for t in range(60_000, end + 1, 60_000):
        count = 1000 + rnd.randint(-20, 20) if t < 2_640_000 else 840 + rnd.randint(-15, 15)
        quality = "missing" if t == 1_200_000 else "ok"
        samples.append({"time_ms": t, "broadcast_ms": t, "live_id": "L1", "segment": 0,
                        "viewer_count": count if quality == "ok" else None, "quality": quality})
    return events, samples, gap, end


def run(legacy, name, config, viewer_adaptive):
    sys.path.insert(0, str(Path(legacy).resolve()))
    from czzedit.common import Config
    from czzedit.live_analyzer import LiveAnalyzer

    events, samples, gap, end = scenario()
    start = 120_000
    an = LiveAnalyzer(start, Config(**config, viewer_adaptive=viewer_adaptive))
    rnd = random.Random(7)
    steps = []
    ei = si = 0
    t = start
    count = 0
    gap_sent = False
    late_pool = []
    while t <= end:
        # Chat up to 5 s past the watermark has arrived.
        batch = []
        while ei < len(events) and events[ei]["time_ms"] < t + 5000:
            e = events[ei]
            ei += 1
            if rnd.random() < 0.01:
                late_pool.append(e)  # delivered a bit later than it should
            else:
                batch.append(e)
        if late_pool and rnd.random() < 0.2:
            batch.extend(late_pool)
            late_pool = []
        if batch:
            an.add(batch)
            steps.append({"op": "add", "events": [{"id": e["event_id"], "seq": e["seq"], "offsetMs": e["time_ms"],
                                                   "authorKey": e["author_key"], "text": e["content"]} for e in batch]})
        while viewer_adaptive and si < len(samples) and samples[si]["time_ms"] <= t + 5000:
            an.sample(samples[si])
            steps.append({"op": "sample", "sample": samples[si]})
            si += 1
        if not gap_sent and t >= gap[1]:
            an.gap(*gap)
            steps.append({"op": "gap", "start": gap[0], "end": gap[1]})
            gap_sent = True
        released = an.advance(t)
        steps.append({"op": "advance", "end": t, "final": False, "expect": [cand(m) for m in released]})
        count += 1
        if count % 25 == 0:
            steps.append({"op": "previews", "expect": [cand(m) for m in an.previews()]})
        t += rnd.choice([2000, 5000, 5000, 7000])
    final = end + 2345
    released = an.advance(final, final=True)
    steps.append({"op": "advance", "end": final, "final": True, "expect": [cand(m) for m in released]})
    case = {"startMs": start, "config": config, "viewerAdaptive": viewer_adaptive, "steps": steps}
    (HERE / f"{name}.steps.json").write_text(json.dumps(case, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")
    n = sum(len(s["expect"]) for s in steps if s["op"] == "advance")
    trans = sum(1 for s in steps if s["op"] == "advance" for c in s["expect"] if c.get("baselineTransition"))
    print(f"{name}: {len(steps)} steps, {n} released candidates, {trans} baseline transitions")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--legacy", required=True)
    args = ap.parse_args()
    run(args.legacy, "live_viewer", {}, True)
    run(args.legacy, "live_sensitive", {"sensitive": True}, False)


if __name__ == "__main__":
    main()

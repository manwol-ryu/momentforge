#!/usr/bin/env python3
"""Regenerates the legacy comparison cases used by TestMatchesLegacy.

  LEGACY=$(mktemp -d) && git archive legacy-python czzedit | tar -x -C "$LEGACY"
  python3 -I internal/detect/rules/testdata/legacy/gen_cases.py --legacy "$LEGACY"

For each case it writes <case>.events.json (input), <case>.case.json (end,
gaps, settings) and <case>.legacy.json (CzzEdit 0.3.2 output via
.claude/skills/port-legacy-module/scripts/legacy_detect.py). All chat is synthetic.
"""
import argparse
import json
import random
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[4]
HARNESS = ROOT / ".claude/skills/port-legacy-module/scripts/legacy_detect.py"


def demo_events():
    """Same rows and order as legacy service.demo_rows and Go domain.DemoChat."""
    rows = []
    for start in range(0, 600000, 10000):
        for i in range(10):
            rows.append((start + i * 800, f"user-{i}", f"평소 대화 {i}"))
    for start, content in ((300000, "ㅋㅋㅋㅋ"), (370000, "다시 보고 싶은 장면"), (450000, "헐")):
        for offset in range(0, 20000, 2000):
            for i in range(18):
                rows.append((start + offset + i * 30, f"reaction-{i}", content))
    for offset in range(100):
        rows.append((520000 + offset * 10, "spammer", "ㅋㅋㅋㅋ"))
    rows.sort(key=lambda r: r[0])
    return [{"id": f"demo-{i + 1:05d}", "seq": i + 1, "offsetMs": t, "authorKey": a, "text": c}
            for i, (t, a, c) in enumerate(rows)]


def mixed_events():
    rnd = random.Random(20261016)
    rows = []
    base_authors = [f"a{i:02d}" for i in range(40)]
    casual = ["오늘 방송 재밌다", "GG", "Nice  play", "이거 뭐임", "ㅇㅇ", "점심 뭐 먹지", "굿굿",
              "lol", "오 좋다", "이번 판 이기자", "화이팅", "음악 좋네"]
    noise = ["!명령어", "/clear", "안녕하세요", "ㅎㅇ", "출첵", "   "]
    for block in range(0, 1500000, 10000):
        for _ in range(rnd.randint(4, 14)):
            t = block + rnd.randrange(10000)
            text = rnd.choice(casual) if rnd.random() > 0.08 else rnd.choice(noise)
            rows.append((t, rnd.choice(base_authors), text))

    def burst(start, length, authors, texts, per_author_gap=(2000, 6000)):
        for a in authors:
            t = start + rnd.randrange(3000)
            while t < start + length:
                rows.append((t, a, rnd.choice(texts)))
                t += rnd.randint(*per_author_gap)

    reactors = [f"r{i:02d}" for i in range(30)]
    burst(360000, 25000, reactors[:22], ["ㅋㅋㅋㅋㅋ", "ㅋㅋ", "ㅎㅎㅎ", "😂😂", "ㅋㅋㅋ 미쳤네"])
    burst(500000, 20000, reactors[:16], ["이거 클립 각", "이거  클립 각", "이거 클립 각!"])
    burst(610000, 20000, reactors[:20], ["ㅋㅋㅋㅋ"])  # inside the gap: must not count
    nfd_wa = "\u110b\u116a"  # "와" typed as conjoining jamo (ᄋ + ᅪ)
    burst(720000, 20000, reactors[:20], ["와", "헐", "미쳤다 ㄷㄷ", "대박이네", nfd_wa, "와 대박"])
    burst(900000, 220000, reactors[:24], ["ㅋㅋㅋㅋ", "ㅋㅋㅋㅋㅋㅋ", "ㅎㅎ"], (3000, 7000))  # long: split at 180 s
    for i in range(80):
        rows.append((1100000 + i * 40, "spammer", "ㅋㅋㅋ"))
    burst(1200000, 20000, reactors[:18], ["ㅠㅠㅠ", "아깝다", "아쉽다 진짜", "ㅜㅜ"])
    for i in range(12):
        rows.append((1300000 + i * 500, None, "작성자 없음"))  # unknown author → invalid window
    for i in range(40):
        rows.append((1400000 + rnd.randrange(9000), f"new{i:02d}", f"처음 왔어요 {i}"))
    rows.sort(key=lambda r: r[0])
    return [{"id": f"mix-{i + 1:05d}", "seq": i + 1, "offsetMs": t, "authorKey": a, "text": c}
            for i, (t, a, c) in enumerate(rows)]


CASES = {
    "demo_default": (demo_events, 600000, [], {}),
    "demo_sensitive": (demo_events, 600000, [], {"sensitive": True}),
    "mixed_default": (mixed_events, 1503337, [[600000, 640000]], {}),
    "mixed_tuned": (mixed_events, 1503337, [[600000, 640000]],
                    {"sensitive": True, "reaction_delay_ms": 3000, "pre_roll_ms": 20000, "post_roll_ms": 45000}),
}


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--legacy", required=True)
    args = ap.parse_args()
    for name, (make, end, gaps, config) in CASES.items():
        events = make()
        events_path = HERE / f"{name}.events.json"
        events_path.write_text(json.dumps(events, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")
        (HERE / f"{name}.case.json").write_text(
            json.dumps({"endMs": end, "gaps": gaps, "config": config}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
        subprocess.run([sys.executable, "-I", str(HARNESS), "--legacy", args.legacy, "--events", str(events_path),
                        "--end", str(end), "--config", json.dumps(config), "--gaps", json.dumps(gaps),
                        "--out", str(HERE / f"{name}.legacy.json")], check=True)
        out = json.loads((HERE / f"{name}.legacy.json").read_text(encoding="utf-8"))
        print(f"{name}: {len(events)} events, {len(out['candidates'])} candidates, {len(out['windows'])} windows")


if __name__ == "__main__":
    main()

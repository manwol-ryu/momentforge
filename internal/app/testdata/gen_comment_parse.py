#!/usr/bin/env python3
"""Regenerates comment_parse.json for TestParseTimestampsMatchesLegacy.

  LEGACY=$(mktemp -d) && git archive legacy-python czzedit | tar -x -C "$LEGACY"
  python3 -I internal/app/testdata/gen_comment_parse.py --legacy "$LEGACY"

Runs CzzEdit 0.3.2's comment timestamp parser on synthetic comments and
stores its results. All comments are made up.
"""
import argparse
import json
import sys
from pathlib import Path

CASES = [
    "1:23 웃긴 장면", "12:34", "1:02:03 레전드", "0:59 시작하자마자", "1:60 잘못된 초", "1:61:00 잘못된 분",
    "1시간 2분 3초 여기", "2분 30초", "1시간2분3초", "10분 61초", "1시간 60분 0초",
    "12:00~13:30 하이라이트", "12:00 ~ 13:30", "12:00 - 11:00 거꾸로", "12:00–12:40 대시", "12:00〜13:00", "1:00:00～1:01:00",
    "오후 3:00에 다시", "오늘 12:00 방송", "5:00 후에 시작", "3:00 남음", "1:00 뒤 반전", "점수 3:2", "비율 16:9 화면",
    "1:23 웃김 2:34 대박 3:45 미침", "1:23 웃김 1:23 웃김 중복", "99:00 영상 끝 넘음", "4:59:59 끝", "5:00:00 끝 바로",
    "a1:23 붙음", "-1:23 음수", "1:23:4 짧음", "12:345 숫자", "1:2 짧음", "타임라인\n0:30 인트로\n5:10 첫 킬\n12:00 ~ 12:45 보스전",
    "3:00 | 웃음 / 4:00 | 눈물", "시계 10:30 맞춤", "날짜 1:12", "내일 7:30", "1:23:45:67", "0:00 처음",
    "🙂 1:23 이모지 뒤", "1:23후 붙은 후", "1:23 후 띄운 후", "앞부분 아주 긴 설명이 있고 나서야 오후 3:00", "100:00 긴 영상",
]
DURATION = 5 * 3600 * 1000


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--legacy", required=True)
    args = ap.parse_args()
    sys.path.insert(0, str(Path(args.legacy).resolve()))
    from czzedit.comments import parse
    out = []
    for text in CASES:
        found, issues = parse(text, DURATION)
        out.append({"text": text, "found": found, "issues": issues})
    path = Path(__file__).resolve().parent / "comment_parse.json"
    path.write_text(json.dumps({"durationMs": DURATION, "cases": out}, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    print(f"{len(out)} cases → {path.name}")


if __name__ == "__main__":
    main()

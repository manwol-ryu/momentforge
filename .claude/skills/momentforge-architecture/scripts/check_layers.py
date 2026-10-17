#!/usr/bin/env python3
"""MomentForge 구조 검사: Go 패키지 의존 방향과 리터럴 위치 규칙.

사용법:
  python3 -I .claude/skills/momentforge-architecture/scripts/check_layers.py [--root DIR]
  python3 -I .../check_layers.py --hook   # Claude Code PostToolUse 훅 (stdin JSON)

규칙 표는 ../SKILL.md 의 "의존 방향", "위치 규칙" 절과 같아야 합니다.
표준 라이브러리만 사용합니다. 종료 코드: 0 통과, 1 위반, 2 훅 모드 위반.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

DEFAULT_MODULE = "github.com/manwol-ryu/momentforge"
SCAN_DIRS = ("cmd", "internal")
SKIP_PARTS = {"testdata", "vendor", "node_modules", ".git"}

# 소스 패키지 패턴 → import 가능한 내부 패키지 (모듈 경로 기준 상대 경로).
# "*" 패턴은 그 아래 한 단계 이름(플랫폼·제공자)마다 하나의 단위로 취급합니다.
ALLOWED: dict[str, tuple[str, ...] | None] = {
    "cmd/*": None,  # None = 모두 허용 (조립 담당)
    "internal/api": ("internal/app", "internal/events", "internal/domain"),
    "internal/mcp": ("internal/app", "internal/domain"),
    "internal/app": (
        "internal/platform", "internal/detect", "internal/store", "internal/ai",
        "internal/export", "internal/events", "internal/privacy", "internal/domain",
    ),
    "internal/platform": ("internal/domain",),
    "internal/platform/*": ("internal/platform", "internal/domain"),
    "internal/detect": ("internal/domain",),
    "internal/detect/rules": ("internal/detect", "internal/domain"),
    "internal/store": ("internal/domain",),
    "internal/export": ("internal/domain",),
    "internal/events": ("internal/domain",),
    "internal/ai": ("internal/domain",),
    "internal/ai/*": ("internal/ai", "internal/domain"),
    "internal/privacy": (),
    "internal/domain": (),
}

# (문자열 리터럴 안에서 찾을 패턴, 허용 경로 접두어, 이유, 고치는 방법)
LITERAL_RULES = [
    (re.compile(r"(?:^|[./])(?:chzzk\.)?naver\.(?:com|net)\b"), ("internal/platform/chzzk/",),
     "플랫폼 도메인이 어댑터 밖에 있습니다",
     "internal/platform/chzzk 안으로 옮기고 다른 곳은 platform 인터페이스를 쓰세요"),
    (re.compile(r"\bclaude-[a-z0-9][a-z0-9.-]*"), ("internal/ai/anthropic/",),
     "Claude 모델 ID가 internal/ai/anthropic 밖에 있습니다",
     "모델 ID 상수는 internal/ai/anthropic에 두고, 화면 목록은 Models API로 조회하세요"),
    (re.compile(r"\bgpt-[0-9a-z][0-9a-z.-]*|api\.openai\.com"), ("internal/ai/openai/",),
     "OpenAI 모델 ID·엔드포인트가 internal/ai/openai 밖에 있습니다",
     "internal/ai/openai 안으로 옮기세요"),
    (re.compile(r"\bopencode-go/"), ("internal/ai/opencode/",),
     "OpenCode Go 모델 ID가 internal/ai/opencode 밖에 있습니다",
     "internal/ai/opencode 안으로 옮기고, 화면 목록은 opencode models로 조회하세요"),
    (re.compile(r"api\.anthropic\.com"), (),
     "Claude API를 직접 HTTP로 호출하려 합니다",
     "anthropic-sdk-go를 쓰세요 (claude-api 스킬 참고)"),
    (re.compile(r"\b0\.0\.0\.0\b"), (),
     "모든 인터페이스에 바인딩하려 합니다",
     "서버는 127.0.0.1에만 바인딩합니다 (privacy-security.md)"),
]

IMPORT_SINGLE = re.compile(r'^\s*import\s+(?:[\w.]+\s+)?"([^"]+)"')
IMPORT_OPEN = re.compile(r"^\s*import\s*\(")
IMPORT_LINE = re.compile(r'^\s*(?:[\w.]+\s+)?"([^"]+)"')
STRING_LIT = re.compile(r'"(?:[^"\\\n]|\\.)*"|`[^`]*`')


def find_root(start: Path) -> Path:
    for p in [start, *start.parents]:
        if (p / ".git").exists() or (p / "go.mod").exists():
            return p
    return start


def module_path(root: Path) -> str:
    mod = root / "go.mod"
    if mod.exists():
        for line in mod.read_text(encoding="utf-8").splitlines():
            if line.startswith("module "):
                return line.split()[1].strip()
    return DEFAULT_MODULE


def match_pattern(pkg: str) -> tuple[str, str] | None:
    """패키지 경로에 맞는 ALLOWED 키와 그 단위 루트를 반환 (가장 구체적인 것 우선)."""
    parts = pkg.split("/")
    best = None
    for key in ALLOWED:
        kp = key.split("/")
        if kp[-1] == "*":
            base = kp[:-1]
            if len(parts) > len(base) and parts[:len(base)] == base:
                unit = "/".join(parts[:len(base) + 1])
                cand = (len(kp), key, unit)
            else:
                continue
        elif parts[:len(kp)] == kp and (len(parts) == len(kp)):
            cand = (len(kp) + 0.5, key, key)  # 정확히 일치하면 와일드카드보다 우선
        else:
            continue
        if best is None or cand[0] > best[0]:
            best = cand
    return (best[1], best[2]) if best else None


def strip_comments(lines: list[str]) -> list[str]:
    out, in_block = [], False
    for line in lines:
        buf, i = [], 0
        while i < len(line):
            if in_block:
                end = line.find("*/", i)
                if end < 0:
                    i = len(line)
                    continue
                in_block, i = False, end + 2
                continue
            if line.startswith("//", i):
                break
            if line.startswith("/*", i):
                in_block, i = True, i + 2
                continue
            if line[i] in "\"`":  # 문자열 안의 // 는 주석이 아님
                m = STRING_LIT.match(line, i)
                if m:
                    buf.append(m.group(0))
                    i = m.end()
                    continue
            buf.append(line[i])
            i += 1
        out.append("".join(buf))
    return out


def imports(lines: list[str]):
    in_block = False
    for no, line in enumerate(lines, 1):
        if in_block:
            if line.strip().startswith(")"):
                in_block = False
                continue
            m = IMPORT_LINE.match(line)
            if m:
                yield no, m.group(1)
            continue
        if IMPORT_OPEN.match(line):
            in_block = True
            continue
        m = IMPORT_SINGLE.match(line)
        if m:
            yield no, m.group(1)


def go_files(root: Path):
    for d in SCAN_DIRS:
        base = root / d
        if not base.is_dir():
            continue
        for f in sorted(base.rglob("*.go")):
            if SKIP_PARTS.intersection(f.relative_to(root).parts):
                continue
            yield f


def check(root: Path) -> tuple[list[str], int]:
    module = module_path(root)
    problems: list[str] = []
    count = 0
    for f in go_files(root):
        count += 1
        rel = f.relative_to(root).as_posix()
        pkg = f.parent.relative_to(root).as_posix()
        lines = strip_comments(f.read_text(encoding="utf-8").splitlines())

        matched = match_pattern(pkg)
        if matched is None:
            problems.append(f"{rel}: 패키지 '{pkg}'가 구조 규칙에 없습니다.\n"
                            f"    고치는 방법: REDESIGN.md 5절의 패키지에 넣거나, 새 패키지가 필요하면 "
                            f"SKILL.md 표와 check_layers.py의 ALLOWED에 함께 추가하세요.")
        else:
            key, unit = matched
            allowed = ALLOWED[key]
            for no, imp in imports(lines):
                if not imp.startswith(module + "/"):
                    continue
                target = imp[len(module) + 1:]
                if allowed is None or target == unit or target.startswith(unit + "/"):
                    continue
                if target in allowed:
                    continue
                problems.append(
                    f"{rel}:{no}: '{pkg}'은(는) '{target}'을(를) import할 수 없습니다 ({key} 규칙).\n"
                    f"    허용: {', '.join(allowed) if allowed else '내부 패키지 없음'}\n"
                    f"    고치는 방법: 인터페이스를 쓰는 쪽 패키지에 두고 구현체는 cmd/momentforge에서 등록하세요.")

        for no, line in enumerate(lines, 1):
            for lit in STRING_LIT.findall(line):
                for pattern, allowed_prefixes, reason, fix in LITERAL_RULES:
                    if pattern.search(lit) and not any(rel.startswith(p) for p in allowed_prefixes):
                        problems.append(f"{rel}:{no}: {reason}: {lit[:60]}\n    고치는 방법: {fix}")
    return problems, count


def find_gofmt() -> str | None:
    """PATH에 없을 때(예: Go 설치 전에 시작한 앱의 훅) 표준 설치 경로도 찾는다."""
    found = shutil.which("gofmt")
    if found:
        return found
    candidates = [Path(os.environ["GOROOT"]) / "bin" / "gofmt"] if os.environ.get("GOROOT") else []
    candidates += [Path("/usr/local/go/bin/gofmt"), Path("/opt/homebrew/bin/gofmt"), Path("C:/Program Files/Go/bin/gofmt.exe")]
    return next((str(p) for p in candidates if p.is_file()), None)


def gofmt_problems(paths: list[Path]) -> list[str]:
    gofmt = find_gofmt()
    if not gofmt or not paths:
        return []
    r = subprocess.run([gofmt, "-l", *map(str, paths)], capture_output=True, text=True)
    return [f"{p}: gofmt 형식이 맞지 않습니다. 고치는 방법: gofmt -w {p}" for p in r.stdout.split()]


def hook_mode(root: Path) -> int:
    try:
        data = json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError):
        return 0
    raw = (data.get("tool_input") or {}).get("file_path") or ""
    if not raw.endswith(".go"):
        return 0
    path = Path(raw)
    try:
        rel = path.resolve().relative_to(root.resolve())
    except ValueError:
        return 0
    if not rel.parts or rel.parts[0] not in SCAN_DIRS:
        return 0
    problems, _ = check(root)
    problems += gofmt_problems([path])
    if problems:
        print("momentforge-architecture 규칙 위반 (수정 후 다시 확인하세요):", file=sys.stderr)
        for p in problems:
            print("- " + p, file=sys.stderr)
        return 2
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description="MomentForge Go 패키지 구조 검사")
    ap.add_argument("--root", type=Path, help="저장소 루트 (기본: 자동 탐색)")
    ap.add_argument("--hook", action="store_true", help="Claude Code PostToolUse 훅 모드")
    args = ap.parse_args()
    root = (args.root or find_root(Path(__file__).resolve().parent)).resolve()

    if args.hook:
        return hook_mode(root)

    problems, count = check(root)
    if count == 0:
        print("검사할 Go 파일이 없습니다 (cmd/, internal/). 건너뜁니다.")
        return 0
    if problems:
        print(f"위반 {len(problems)}건 (Go 파일 {count}개 검사):")
        for p in problems:
            print("- " + p)
        return 1
    print(f"통과: Go 파일 {count}개, 의존 방향·위치 규칙 위반 없음.")
    return 0


if __name__ == "__main__":
    sys.exit(main())

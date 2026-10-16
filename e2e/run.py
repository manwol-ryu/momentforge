"""Runs every test_* function in e2e/test_*.py against a running MomentForge.

Usage (normally via `make e2e`, which starts the server with with_server.py):
    e2e/.venv/bin/python e2e/run.py --base-url http://127.0.0.1:18765 [-k name]

Each test gets a fresh browser context and page: fn(page, base_url).
A test fails on an exception or on any browser console error, except ones
containing a text listed in the test's `allow_console` attribute (for
expected API error responses). Failure screenshots go to e2e/.data/screenshots/.
"""
from __future__ import annotations

import argparse
import importlib.util
import sys
import time
import traceback
from pathlib import Path

from playwright.sync_api import sync_playwright

HERE = Path(__file__).resolve().parent
SHOTS = HERE / ".data" / "screenshots"


def discover(pattern: str | None):
    for path in sorted(HERE.glob("test_*.py")):
        spec = importlib.util.spec_from_file_location(path.stem, path)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        for name in sorted(dir(module)):
            fn = getattr(module, name)
            if name.startswith("test_") and callable(fn):
                full = f"{path.stem}.{name}"
                if not pattern or pattern in full:
                    yield full, fn


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base-url", required=True)
    ap.add_argument("-k", dest="pattern", help="run tests whose name contains this text")
    args = ap.parse_args()

    tests = list(discover(args.pattern))
    if not tests:
        print("실행할 테스트가 없습니다.")
        return 1
    SHOTS.mkdir(parents=True, exist_ok=True)
    failures = []
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        for name, fn in tests:
            context = browser.new_context(locale="ko-KR")
            page = context.new_page()
            console_errors: list[str] = []
            page.on("console", lambda m: console_errors.append(m.text) if m.type == "error" else None)
            page.on("pageerror", lambda e: console_errors.append(str(e)))
            started = time.monotonic()
            try:
                fn(page, args.base_url)
                allowed = getattr(fn, "allow_console", [])
                console_errors = [e for e in console_errors if not any(a in e for a in allowed)]
                if console_errors:
                    raise AssertionError("브라우저 콘솔 오류: " + " | ".join(console_errors))
                print(f"통과  {name} ({time.monotonic() - started:.1f}s)")
            except Exception:
                shot = SHOTS / f"{name}-fail.png"
                try:
                    page.screenshot(path=str(shot), full_page=True)
                except Exception:
                    shot = None
                failures.append(name)
                print(f"실패  {name}")
                print("      " + traceback.format_exc().strip().replace("\n", "\n      "))
                if shot:
                    print(f"      스크린샷: {shot}")
            finally:
                context.close()
        browser.close()

    print(f"\n{len(tests) - len(failures)}/{len(tests)} 통과")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())

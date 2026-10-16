"""Phase 0: the single binary serves the app, the API is guarded, SSE connects."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"


def open_home(page: Page, base_url: str) -> None:
    page.goto(base_url)
    # SSE keeps a request open, so networkidle never comes; the app sets this instead.
    page.wait_for_selector('body[data-app-state="ready"]', timeout=10_000)


def test_home_shows_status_and_live_connection(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    expect(page.get_by_role("heading", level=1, name="MomentForge")).to_be_visible()
    expect(page.get_by_role("heading", name="저장한 작업")).to_be_visible()
    expect(page.locator(".connection")).to_have_text("연결됨", timeout=10_000)
    expect(page.get_by_text(re.compile(r"^스키마 \d+$"))).to_be_visible()
    expect(page.locator(".facts")).to_contain_text("치지직")  # supported platforms
    expect(page.get_by_text(".data", exact=False).first).to_be_visible()  # data folder is e2e/.data
    page.screenshot(path=str(SHOTS / "home-light.png"), full_page=True)


def test_api_requires_access_key(page: Page, base_url: str) -> None:
    res = page.request.get(base_url + "/api/status")
    assert res.status == 401, res.status
    assert res.json()["error"]["code"] == "BAD_KEY"


def test_unknown_route_falls_back_to_app(page: Page, base_url: str) -> None:
    page.goto(base_url + "/vod/123")
    page.wait_for_selector('body[data-app-state="ready"]', timeout=10_000)
    expect(page.get_by_role("heading", level=1)).to_have_text("MomentForge")


def test_mobile_width_has_no_horizontal_scroll(page: Page, base_url: str) -> None:
    page.set_viewport_size({"width": 375, "height": 812})
    open_home(page, base_url)
    overflow = page.evaluate("document.documentElement.scrollWidth - document.documentElement.clientWidth")
    assert overflow <= 0, f"horizontal overflow {overflow}px"
    page.screenshot(path=str(SHOTS / "home-mobile.png"), full_page=True)


def test_dark_mode_uses_dark_tokens(page: Page, base_url: str) -> None:
    page.emulate_media(color_scheme="dark")
    open_home(page, base_url)
    bg = page.evaluate("getComputedStyle(document.body).backgroundColor")
    assert bg == "rgb(35, 34, 34)", bg  # --paper dark #232222
    page.screenshot(path=str(SHOTS / "home-dark.png"), full_page=True)


def test_keyboard_focus_is_visible(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    # No interactive controls yet in phase 0; the focus style must still exist for later controls.
    has_rule = page.evaluate(
        """[...document.styleSheets].some(s => { try { return [...s.cssRules].some(r => r.selectorText === ':focus-visible'); } catch { return false; } })"""
    )
    assert has_rule, ":focus-visible style missing"

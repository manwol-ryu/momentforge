"""Phase 0: the single binary serves the app, the API is guarded, SSE connects.
The start screen is the new-task screen beside the saved-work list."""
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
    expect(page.get_by_role("heading", level=1, name="새 작업")).to_be_visible()
    expect(page.get_by_role("heading", name="저장한 작업")).to_be_visible()  # the sidebar
    expect(page.get_by_role("link", name="새 작업")).to_have_attribute("aria-current", "page")
    expect(page.locator(".connection")).to_have_text("연결됨", timeout=10_000)
    # The first visit explains the steps and offers the sample; with saved work the side lists it instead.
    # Either way the app's facts live in 설정.
    if page.locator(".session-row").count() == 0:
        expect(page.get_by_role("heading", name="이렇게 진행됩니다")).to_be_visible()
        expect(page.get_by_role("heading", name="처음이라면")).to_be_visible()
    else:
        expect(page.get_by_role("heading", name="최근 작업")).to_be_visible()
        expect(page.get_by_role("heading", name="이렇게 진행됩니다")).to_have_count(0)
    expect(page.get_by_role("heading", name="앱 정보")).to_have_count(0)
    page.screenshot(path=str(SHOTS / "home-light.png"), full_page=True)
    page.get_by_role("link", name="설정").first.click()
    page.wait_for_selector('body[data-app-state="ready"]', timeout=10_000)
    facts = page.locator("section", has=page.get_by_role("heading", name="앱 정보"))
    expect(facts.get_by_text(re.compile(r"^스키마 \d+$"))).to_be_visible()
    expect(facts).to_contain_text("치지직")  # supported platforms
    expect(facts).to_contain_text(".data")  # data folder is e2e/.data


def test_api_requires_access_key(page: Page, base_url: str) -> None:
    res = page.request.get(base_url + "/api/status")
    assert res.status == 401, res.status
    assert res.json()["error"]["code"] == "BAD_KEY"


def test_unknown_route_falls_back_to_app(page: Page, base_url: str) -> None:
    page.goto(base_url + "/vod/123")
    page.wait_for_selector('body[data-app-state="ready"]', timeout=10_000)
    expect(page.get_by_role("heading", level=1)).to_have_text("새 작업")


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
    assert bg == "rgb(28, 28, 28)", bg  # --paper dark #1c1c1c (Tracwell's dark background)
    page.screenshot(path=str(SHOTS / "home-dark.png"), full_page=True)


def test_keyboard_focus_is_visible(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    # No interactive controls yet in phase 0; the focus style must still exist for later controls.
    has_rule = page.evaluate(
        """[...document.styleSheets].some(s => { try { return [...s.cssRules].some(r => r.selectorText === ':focus-visible'); } catch { return false; } })"""
    )
    assert has_rule, ":focus-visible style missing"


PASTE = """(el, text) => {
    const data = new DataTransfer();
    data.setData("text/plain", text);
    el.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
}"""


def test_address_of_the_other_kind_switches_the_tab(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    vod_tab = page.get_by_role("tab", name="다시보기")
    live_tab = page.get_by_role("tab", name="생방송")
    expect(vod_tab).to_have_attribute("aria-selected", "true")
    # Pasting a live address into the VOD field moves to the live tab and its field.
    page.get_by_label("치지직 다시보기 주소").evaluate(PASTE, "https://chzzk.naver.com/live/abc")
    expect(live_tab).to_have_attribute("aria-selected", "true")
    expect(page.get_by_role("status").filter(has_text="생방송 주소라서 생방송 수집으로 옮겼습니다")).to_be_visible()
    expect(page.get_by_label("치지직 생방송 주소")).to_be_focused()
    # A typed address of the other kind gets a note with the same move; the address comes along.
    vod_tab.click()
    page.get_by_label("치지직 다시보기 주소").fill("https://chzzk.naver.com/live/abc")
    expect(page.get_by_text("생방송 주소로 보입니다.")).to_be_visible()
    page.get_by_role("button", name="생방송 수집으로 옮기기").click()
    expect(live_tab).to_have_attribute("aria-selected", "true")
    expect(page.get_by_label("치지직 생방송 주소")).to_have_value("https://chzzk.naver.com/live/abc")
    # Arrow keys move between the tabs.
    live_tab.focus()
    page.keyboard.press("ArrowLeft")
    expect(vod_tab).to_have_attribute("aria-selected", "true")
    expect(vod_tab).to_be_focused()

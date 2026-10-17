"""Motion (docs/DESIGN-SYSTEM.md 8): with motion allowed, the home title
reveals word by word, the timeline sweeps in, candidate rows rise in, the
source pill slides to the chosen segment and the count rolls; with reduced
motion the same screens settle at once. The other E2E tests run with reduced
motion so clicks and screenshots never land mid-animation."""
from __future__ import annotations

from pathlib import Path

from playwright.sync_api import Page, expect

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'


def animation(page: Page, selector: str) -> str:
    return page.locator(selector).first.evaluate("e => getComputedStyle(e).animationName")


def test_motion_follows_state_changes(page: Page, base_url: str) -> None:
    page.emulate_media(reduced_motion="no-preference")
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    assert animation(page, ".reveal-w") == "mf-rise"
    expect(page.locator(".reveal-w")).to_have_count(6)  # MomentForge + five words of the lede
    expect(page.get_by_role("heading", level=1)).to_have_text("MomentForge")

    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    assert animation(page, ".timeline-track") == "mf-sweep"
    assert animation(page, ".timeline-marker") == "mf-pop"
    assert animation(page, "ol.candidates > li") == "mf-rise-sm"
    # Rows enter one after another: the second starts 40 ms after the first.
    delays = page.locator("ol.candidates > li").evaluate_all("els => els.slice(0, 2).map(e => getComputedStyle(e).animationDelay)")
    assert delays == ["0s", "0.04s"], delays
    expect(page.get_by_role("heading", name="편집 후보 3개")).to_be_visible()
    page.wait_for_timeout(1200)
    page.screenshot(path=str(SHOTS / "motion-session.png"))


def test_page_bar_appears_after_the_title(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    bar = page.locator(".page-bar")
    expect(bar).to_be_hidden()  # hidden and inert until the title scrolls away
    page.get_by_role("heading", name="내보내기").scroll_into_view_if_needed()
    expect(bar).to_be_visible()
    expect(bar).to_contain_text("샘플 채팅 (실제 방송 아님)")
    page.screenshot(path=str(SHOTS / "page-bar.png"))
    bar.get_by_role("link", name="MomentForge").click()
    expect(page.get_by_role("heading", level=1, name="MomentForge")).to_be_visible()

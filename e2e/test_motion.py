"""Motion (docs/DESIGN-SYSTEM.md 8): with motion allowed, the start title
reveals word by word, the timeline sweeps in, candidate rows rise in, the
line under the tabs slides to the chosen tab and the count rolls; with
reduced motion the same screens settle at once. The other E2E tests run with
reduced motion so clicks and screenshots never land mid-animation."""
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
    expect(page.locator(".reveal-w")).to_have_count(7)  # 새 작업 + five words of the lede
    expect(page.get_by_role("heading", level=1)).to_have_text("새 작업")

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


def test_tab_line_slides_to_the_chosen_tab(page: Page, base_url: str) -> None:
    page.emulate_media(reduced_motion="no-preference")
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    panel = page.locator(".ws-panel")
    ink = panel.locator(".tab-ink")
    expect(panel.locator(".tab-list[data-ready]")).to_have_count(1)  # placed first, then animated
    assert ink.evaluate("e => getComputedStyle(e).transitionDuration") == "0.56s, 0.56s"
    files = panel.get_by_role("tab", name="파일")
    files.click()
    page.wait_for_timeout(800)
    tab_box, ink_box = files.bounding_box(), ink.bounding_box()
    assert abs(tab_box["x"] - ink_box["x"]) < 1 and abs(tab_box["width"] - ink_box["width"]) < 1, (tab_box, ink_box)
    page.screenshot(path=str(SHOTS / "tabs.png"))

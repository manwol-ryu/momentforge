"""Phase 1: the sample analysis shows the legacy demo's three candidates on a timeline."""
from __future__ import annotations

from pathlib import Path

from playwright.sync_api import Page, expect

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'


def open_demo_from_home(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("link", name="샘플로 먼저 체험").click()
    expect(page).to_have_url(base_url + "/demo")
    expect(page.get_by_role("heading", level=1, name="샘플 채팅")).to_be_visible()
    page.wait_for_selector(READY, timeout=10_000)


def test_demo_lists_three_candidates_with_reasons(page: Page, base_url: str) -> None:
    open_demo_from_home(page, base_url)
    expect(page.get_by_role("heading", name="편집 후보 3개")).to_be_visible()
    items = page.locator("ol.candidates > li")
    expect(items).to_have_count(3)
    # Same moments and reasons as CzzEdit 0.3.2's demo (laugh, repeat, surprise, plus volume).
    expect(items.nth(0)).to_contain_text("04:55–05:25")
    expect(items.nth(0)).to_contain_text("웃음 증가")
    expect(items.nth(1)).to_contain_text("집단 반복")
    expect(items.nth(2)).to_contain_text("감정 반응")
    for i in range(3):
        expect(items.nth(i)).to_contain_text("채팅 급증")
    expect(page.get_by_role("img", name="반응 밀도 그래프")).to_be_visible()
    page.screenshot(path=str(SHOTS / "demo-light.png"), full_page=True)


def test_selecting_candidate_shows_evidence_and_marks_timeline(page: Page, base_url: str) -> None:
    open_demo_from_home(page, base_url)
    page.get_by_role("button", name="후보 2", exact=False).first.click()
    second = page.locator("ol.candidates > li").nth(1)
    expect(second.get_by_role("button")).to_have_attribute("aria-pressed", "true")
    expect(second).to_contain_text("근거 채팅 210건")
    # The shared phrase leads the evidence summary, ahead of background chat.
    expect(second.locator(".evidence li").first).to_contain_text("다시 보고 싶은 장면 180건")
    # The matching timeline marker is pressed too.
    expect(page.locator(".timeline-marker").nth(1)).to_have_attribute("aria-pressed", "true")
    page.screenshot(path=str(SHOTS / "demo-selected.png"), full_page=True)


def test_timeline_marker_is_keyboard_operable(page: Page, base_url: str) -> None:
    open_demo_from_home(page, base_url)
    marker = page.get_by_role("button", name="후보 3, 07:25")
    marker.focus()
    page.keyboard.press("Enter")
    expect(marker).to_have_attribute("aria-pressed", "true")
    expect(page.locator("ol.candidates > li").nth(2)).to_contain_text("근거 채팅")


def test_demo_page_loads_directly_and_back_link_works(page: Page, base_url: str) -> None:
    page.goto(base_url + "/demo")
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("link", name="처음으로").click()
    expect(page.get_by_role("heading", level=1, name="MomentForge")).to_be_visible()


def test_demo_dark_and_mobile(page: Page, base_url: str) -> None:
    page.emulate_media(color_scheme="dark")
    page.set_viewport_size({"width": 375, "height": 812})
    open_demo_from_home(page, base_url)
    overflow = page.evaluate("document.documentElement.scrollWidth - document.documentElement.clientWidth")
    assert overflow <= 0, f"horizontal overflow {overflow}px"
    page.screenshot(path=str(SHOTS / "demo-dark-mobile.png"), full_page=True)

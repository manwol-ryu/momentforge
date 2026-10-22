"""The app frame and the workbench (docs/DESIGN-SYSTEM.md 4): the saved-work
list folds into a rail and stays folded, it becomes a drawer on phones, the
workbench fits the window, and the timeline's playhead moves the chat view
of a session without a video."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'


def open_sample(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)


def test_list_folds_into_a_rail_and_stays_folded(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    nav = page.get_by_role("navigation", name="저장한 작업")
    expect(nav).to_be_visible()
    page.get_by_role("button", name="작업 목록 접기").click()
    expect(nav).to_be_hidden()
    page.reload()
    page.wait_for_selector(READY, timeout=10_000)
    expect(nav).to_be_hidden()  # remembered in this browser
    page.screenshot(path=str(SHOTS / "workbench-folded.png"))
    page.get_by_role("link", name="새 작업").click()  # the rail keeps the way to a new task
    expect(page.get_by_role("heading", level=1, name="새 작업")).to_be_visible()
    page.get_by_role("button", name="작업 목록 펼치기").click()
    expect(nav).to_be_visible()


def test_workbench_fits_the_window(page: Page, base_url: str) -> None:
    page.set_viewport_size({"width": 1440, "height": 900})
    open_sample(page, base_url)
    # The page itself does not scroll; the tool panel scrolls inside.
    assert page.evaluate("document.documentElement.scrollHeight") <= 900
    expect(page.get_by_role("img", name=re.compile("반응 밀도 그래프"))).to_be_in_viewport()
    panel = page.get_by_role("tabpanel", name=re.compile("^후보"))
    assert panel.evaluate("e => getComputedStyle(e).overflowY") == "auto"
    page.screenshot(path=str(SHOTS / "workbench.png"))


def test_playhead_moves_the_chat_without_a_video(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    chat = page.get_by_role("list", name="고른 위치 주변 채팅")
    expect(chat.locator("li").first).to_contain_text("00:")
    # A click on the track moves the playhead there (the middle of ten minutes), the arrow keys by 5 seconds.
    slider = page.get_by_role("slider", name="채팅 볼 위치")
    box = slider.bounding_box()
    slider.click(position={"x": box["width"] / 2, "y": box["height"] / 2})
    expect(page.locator(".stage-timeline .stage-time")).to_have_text("05:00 / 10:00")
    expect(chat.locator("li[data-current] .chat-time")).to_have_text(re.compile(r"^(04:[345]\d|05:00)$"))
    page.keyboard.press("ArrowRight")
    expect(page.locator(".stage-timeline .stage-time")).to_have_text("05:05 / 10:00")
    # Choosing a candidate moves the playhead to its clip.
    page.locator("ol.candidates > li").nth(2).get_by_role("button").first.click()
    expect(page.locator(".stage-timeline .stage-time")).to_contain_text("06:55")
    # A chat line is a button: pressing it moves the playhead to that message.
    expect(chat.locator("li[data-current] .chat-time")).to_have_text(re.compile(r"^06:5\d$"))  # the chat followed the clip
    row = chat.locator("li").first
    at = row.locator(".chat-time").inner_text()
    row.get_by_role("button").click()
    expect(page.locator(".stage-timeline .stage-time")).to_have_text(f"{at} / 10:00")


def test_next_candidate_keys_and_timeline_zoom(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    stage = page.locator(".stage-timeline .stage-time")
    rows = page.locator("ol.candidates > li")
    # N and P go through the candidates in time order from the playhead, by key position.
    page.locator("body").press("n")
    expect(rows.nth(0)).to_have_attribute("data-selected", "true")
    expect(stage).to_contain_text("04:25")
    page.keyboard.press("n")
    expect(rows.nth(1)).to_have_attribute("data-selected", "true")
    page.get_by_role("button", name="이전 후보").click()
    expect(rows.nth(0)).to_have_attribute("data-selected", "true")
    page.get_by_role("button", name="다음 후보").click()
    page.get_by_role("button", name="다음 후보").click()
    expect(rows.nth(2)).to_have_attribute("data-selected", "true")
    expect(page.get_by_role("button", name="다음 후보")).to_be_disabled()  # the last one
    # Zoomed to 2 minutes around the playhead: fewer markers, and the overview shows where.
    page.get_by_role("radio", name="2분").check()
    expect(page.locator(".timeline-overview")).to_be_visible()
    assert page.locator(".timeline-marker").count() < 3
    page.locator(".stage-timeline").screenshot(path=str(SHOTS / "timeline-zoom.png"))
    # Scoped: a cut plan's boundary switch has a "전체" choice too.
    page.get_by_role("group", name="타임라인 확대").get_by_role("radio", name="전체").check()
    expect(page.locator(".timeline-overview")).to_have_count(0)
    expect(page.locator(".timeline-marker")).to_have_count(3)


def test_tabs_keep_their_state(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    # The workbench's tabs: no AI review tab (it is the line above the candidates now).
    expect(page.get_by_role("tab")).to_have_text([re.compile("^후보"), "대본", "댓글", "파일"])
    page.get_by_role("tab", name="대본").click()
    page.get_by_label("대본 찾기").fill("벌칙")
    page.get_by_role("tab", name=re.compile("^후보")).click()
    page.get_by_role("tab", name="대본").click()
    expect(page.get_by_label("대본 찾기")).to_have_value("벌칙")


def test_phone_list_is_a_drawer(page: Page, base_url: str) -> None:
    page.set_viewport_size({"width": 375, "height": 812})
    open_sample(page, base_url)
    sample = page.get_by_role("link", name="샘플 채팅 (실제 방송 아님)")
    expect(sample).to_be_hidden()
    menu = page.get_by_role("button", name="작업 목록")
    menu.click()
    expect(menu).to_have_attribute("aria-expanded", "true")
    expect(sample).to_be_visible()
    expect(page.locator("#sidebar .new-task")).to_be_focused()
    page.screenshot(path=str(SHOTS / "drawer-mobile.png"))
    page.keyboard.press("Escape")
    expect(sample).to_be_hidden()
    expect(menu).to_be_focused()
    # Opening work from the drawer closes it.
    menu.click()
    page.get_by_role("link", name="새 작업").click()
    expect(page.get_by_role("heading", level=1, name="새 작업")).to_be_visible()
    expect(menu).to_have_attribute("aria-expanded", "false")

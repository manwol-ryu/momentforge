"""Phases 1–2: the sample is stored as a session and reviewed on the same
screen as real VODs (timeline, candidates, context chat, downloads)."""
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
    expect(page).to_have_url(re.compile(r"/sessions/[0-9a-f]{32}$"))
    expect(page.get_by_role("heading", level=1, name="샘플 채팅 (실제 방송 아님)")).to_be_visible()
    page.wait_for_selector(READY, timeout=10_000)


def test_sample_session_lists_three_candidates(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    expect(page.get_by_role("heading", name="편집 후보 3개")).to_be_visible()
    items = page.locator("ol.candidates > li")
    expect(items).to_have_count(3)
    # Same moments and reasons as CzzEdit 0.3.2's demo.
    expect(items.nth(0)).to_contain_text("04:55–05:25")
    expect(items.nth(0)).to_contain_text("웃음 증가")
    expect(items.nth(1)).to_contain_text("집단 반복")
    expect(items.nth(2)).to_contain_text("감정 반응")
    expect(page.get_by_role("img", name=re.compile("반응 밀도 그래프"))).to_be_visible()
    # No video for the sample: no player, no platform link.
    expect(page.get_by_label("다시보기 영상")).to_have_count(0)
    page.screenshot(path=str(SHOTS / "session-sample.png"), full_page=True)


def test_selected_candidate_shows_evidence_and_context_chat(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    page.locator("ol.candidates > li").nth(1).get_by_role("button").first.click()
    second = page.locator("ol.candidates > li").nth(1)
    expect(second.get_by_role("button").first).to_have_attribute("aria-pressed", "true")
    expect(second.locator(".evidence li").first).to_contain_text("다시 보고 싶은 장면 180건")
    context = second.get_by_role("list", name="편집 구간 채팅")
    expect(context).to_be_visible()
    expect(second).to_contain_text("편집 구간 채팅")
    expect(context.locator("li[data-evidence]").first).to_be_visible()
    expect(page.locator(".timeline-marker").nth(1)).to_have_attribute("aria-pressed", "true")
    page.screenshot(path=str(SHOTS / "session-selected.png"), full_page=True)


def test_timeline_marker_is_keyboard_operable(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    marker = page.get_by_role("button", name="후보 3, 07:25")
    marker.focus()
    page.keyboard.press("Enter")
    expect(marker).to_have_attribute("aria-pressed", "true")


def test_downloads_chat_file(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    with page.expect_download() as info:
        page.get_by_role("button", name="chat.txt").click()
    path = info.value.path()
    assert info.value.suggested_filename == "chat.txt"
    assert Path(path).read_text(encoding="utf-8").startswith("# MomentForge chat v1")


def test_sample_is_saved_once_and_reopened(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    first = page.url
    page.get_by_role("link", name="처음으로").click()
    page.wait_for_selector(READY, timeout=10_000)
    expect(page.locator(".sessions").get_by_role("link", name="샘플 채팅 (실제 방송 아님)")).to_have_count(1)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    expect(page).to_have_url(first)


def test_reanalyze_with_sensitivity(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    page.locator(".reanalyze").get_by_label("소규모 채널용 민감도").check()
    page.get_by_role("button", name="다시 분석").click()
    # The job finishes and the screen reloads the new analysis.
    expect(page.locator(".job")).to_contain_text("분석이 끝났습니다", timeout=10_000)
    expect(page.locator(".reanalyze").get_by_label("소규모 채널용 민감도")).to_be_checked()


def test_invalid_urls_are_explained(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    field = page.get_by_label("치지직 다시보기 주소")
    field.fill("https://chzzk.naver.com/live/abc")
    page.get_by_role("button", name="수집하고 편집점 찾기").click()
    expect(page.get_by_role("alert")).to_contain_text("https://chzzk.naver.com/video/영상번호 형식")
    field.fill("https://example.com/video/1")
    page.get_by_role("button", name="수집하고 편집점 찾기").click()
    expect(page.get_by_role("alert")).to_contain_text("지원: 치지직")


# The browser logs the expected 400 responses as resource errors.
test_invalid_urls_are_explained.allow_console = ["status of 400"]


def test_session_dark_and_mobile(page: Page, base_url: str) -> None:
    page.emulate_media(color_scheme="dark")
    page.set_viewport_size({"width": 375, "height": 812})
    open_sample(page, base_url)
    page.locator("ol.candidates > li").nth(0).get_by_role("button").first.click()
    overflow = page.evaluate("document.documentElement.scrollWidth - document.documentElement.clientWidth")
    assert overflow <= 0, f"horizontal overflow {overflow}px"
    page.screenshot(path=str(SHOTS / "session-dark-mobile.png"), full_page=True)

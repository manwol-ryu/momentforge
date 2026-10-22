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


def test_candidates_by_strength(page: Page, base_url: str) -> None:
    """The sample is analyzed as a whole broadcast, so its candidates are ranked:
    each row shows its timeline number, strength and what stood out against the
    usual; the list sorts by strength and hides weak ones (the timeline marks what
    the list shows, and the choice is remembered); the strong and medium ones
    can be picked for the AI review at once; the bars read against the usual."""
    open_sample(page, base_url)
    rows = page.locator("ol.candidates > li")
    expect(rows).to_have_count(3)
    expect(rows.locator(".candidate-no")).to_have_text(["후보 1", "후보 2", "후보 3"])
    strengths = rows.locator(".candidate-strength").all_inner_texts()
    assert len(strengths) == 3 and set(strengths) <= {"강함", "보통", "약함"}, strengths
    expect(rows.first.locator(".candidate-meta")).to_contain_text(re.compile(r"\d+초에 .+\(평소 "))
    expect(page.locator(".stat", has_text="강한 후보").locator("dd")).to_have_text(f"{strengths.count('강함')}개")

    page.get_by_role("radio", name="강한 순").check()
    expect(rows.locator(".candidate-rank")).to_have_text(["1위", "2위", "3위"])
    kept = len([s for s in strengths if s != "약함"])
    page.get_by_role("radio", name="강함·보통만").check()
    expect(rows).to_have_count(kept)
    expect(page.locator(".timeline-marker")).to_have_count(kept)
    expect(page.get_by_role("heading", name=f"편집 후보 {kept}개")).to_be_visible()
    page.locator(".ws-panel").screenshot(path=str(SHOTS / "candidates-ranked.png"))
    page.reload()
    page.wait_for_selector(READY, timeout=10_000)
    expect(page.get_by_role("radio", name="강함·보통만")).to_be_checked()
    expect(rows).to_have_count(kept)
    page.get_by_role("radio", name="모든 강도").check()
    expect(rows).to_have_count(3)

    page.get_by_role("button", name=f"강함·보통 {kept}개 AI 검토에 고르기").click()
    expect(page.get_by_text(f"AI 검토에 고른 후보 {kept}개")).to_be_visible()
    expect(page.get_by_role("button", name="강함·보통 후보를 모두 골랐습니다")).to_be_disabled()
    page.get_by_role("button", name="고른 후보 비우기").click()
    expect(page.locator(".pick-bar")).to_have_count(0)

    graph = page.get_by_role("img", name=re.compile("반응 밀도 그래프"))
    expect(graph).to_have_attribute("data-scale", "score")
    page.get_by_role("radio", name="채팅 수", exact=True).check()
    expect(graph).to_have_attribute("data-scale", "count")
    page.locator(".stage-timeline").screenshot(path=str(SHOTS / "timeline-count.png"))


def test_timeline_marker_is_keyboard_operable(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    marker = page.get_by_role("button", name="후보 3, 07:25")
    marker.focus()
    page.keyboard.press("Enter")
    expect(marker).to_have_attribute("aria-pressed", "true")


def test_downloads_chat_file(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    page.get_by_role("tab", name="파일").click()
    with page.expect_download() as info:
        page.get_by_role("button", name="chat.txt").click()
    path = info.value.path()
    assert info.value.suggested_filename == "chat.txt"
    assert Path(path).read_text(encoding="utf-8").startswith("# MomentForge chat v1")


def test_sample_is_saved_once_and_reopened(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    first = page.url
    row = page.locator(".sessions").get_by_role("link", name="샘플 채팅 (실제 방송 아님)")
    expect(row).to_have_count(1)
    expect(row).to_have_attribute("aria-current", "page")  # the open work is marked in the list
    page.get_by_role("link", name="새 작업").click()
    page.wait_for_selector(READY, timeout=10_000)
    expect(row).to_have_count(1)
    expect(row).not_to_have_attribute("aria-current", "page")
    # Saved work is offered again on the start screen, and the first-visit steps are gone.
    recent = page.locator("section", has=page.get_by_role("heading", name="최근 작업"))
    expect(recent.get_by_role("link", name=re.compile("샘플 채팅"))).to_be_visible()
    expect(page.get_by_role("heading", name="이렇게 진행됩니다")).to_have_count(0)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    expect(page).to_have_url(first)


def test_reanalyze_with_sensitivity(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    page.locator(".reanalyze").get_by_label("항상 가장 민감하게").check()
    page.get_by_role("button", name="다시 분석").click()
    # The job finishes and the screen reloads the new analysis.
    expect(page.locator(".job")).to_contain_text("분석이 끝났습니다", timeout=10_000)
    expect(page.locator(".reanalyze").get_by_label("항상 가장 민감하게")).to_be_checked()


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

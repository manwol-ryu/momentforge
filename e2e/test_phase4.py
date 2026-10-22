"""Phase 4: viewer recommendations from comments, the unified timeline, the
opt-in AI review and hiding the saved list. Uses the E2E binary's synthetic
VOD (https://live.e2e.test/video/{id}) and synthetic AI provider, so nothing
leaves the machine."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'
VIDEO = "https://live.e2e.test/video/7"


def open_home(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)


def select_row(row) -> None:
    """AI results open in full only under the chosen row: choose it (pressing a chosen row closes it)."""
    head = row.locator(".candidate-head")
    if head.get_attribute("aria-pressed") != "true":
        head.click()
    expect(head).to_have_attribute("aria-pressed", "true")


def open_video(page: Page, base_url: str, title: str, url: str) -> None:
    """Opens the saved video, collecting it first if this run has not."""
    open_home(page, base_url)
    row = page.locator(".session-row", has=page.get_by_role("link", name=title))
    if row.count() == 0:
        page.get_by_label("치지직 다시보기 주소").fill(url)
        page.get_by_role("button", name="수집하고 편집점 찾기").click()
        # The screen moves to the saved work as soon as the job has made it, and the candidates follow.
        expect(page).to_have_url(re.compile(r"/sessions/[0-9a-f]{32}$"), timeout=30_000)
        page.wait_for_selector(READY, timeout=10_000)
        expect(page.locator("ol.candidates > li").first).to_be_visible(timeout=30_000)
    else:
        row.get_by_role("link").click()
    expect(page).to_have_url(re.compile(r"/sessions/[0-9a-f]{32}$"))
    page.wait_for_selector(READY, timeout=10_000)


def test_vod_comments_join_the_unified_timeline(page: Page, base_url: str) -> None:
    open_video(page, base_url, "E2E 가짜 다시보기 7", VIDEO)
    expect(page.get_by_role("heading", level=1, name="E2E 가짜 다시보기 7")).to_be_visible()
    viewer = page.locator("ol.candidates > li[data-source='viewer_comment']")
    expect(viewer).to_have_count(2)
    expect(viewer.first).to_contain_text("웃음 터짐")
    expect(viewer.first).to_contain_text("영상 댓글: 12:00 웃음 터짐")
    expect(page.locator("ol.candidates > li[data-source='rule']").first).to_contain_text("웃음 증가")
    expect(page.get_by_role("button", name="시청자 추천 1, 12:00")).to_be_visible()
    page.get_by_role("tab", name="댓글").click()
    expect(page.locator(".comments-summary")).to_contain_text("영상 댓글 4개에서 시각 2개를 찾았습니다")
    expect(page.get_by_text("확인이 필요한 시각 1개")).to_be_visible()
    page.get_by_role("tab", name=re.compile("^후보")).click()

    page.get_by_role("radio", name="시청자 추천").check()
    expect(page.get_by_role("heading", name="편집 후보 2개")).to_be_visible()
    expect(page.locator("ol.candidates > li")).to_have_count(2)
    page.get_by_role("radio", name="모든 출처").check()
    page.screenshot(path=str(SHOTS / "phase4-session.png"), full_page=True)

    page.get_by_role("tab", name="파일").click()
    with page.expect_download() as info:
        page.get_by_role("button", name="모든 출처 편집 후보 TXT").click()
    text = Path(info.value.path()).read_text(encoding="utf-8")
    assert "[시청자 추천] 웃음 터짐" in text and "[자동 탐지]" in text, text[:300]
    with page.expect_download() as info:
        page.get_by_role("button", name="comments.jsonl").click()
    jsonl = Path(info.value.path()).read_text(encoding="utf-8")
    assert "fan-" not in jsonl and "비밀" not in jsonl, "original author ids or hidden comments were exported"


def test_ai_review_is_previewed_then_confirmed(page: Page, base_url: str) -> None:
    open_video(page, base_url, "E2E 가짜 다시보기 7", VIDEO)
    page.locator("ol.candidates > li").first.get_by_label("AI 검토에 포함").check()
    # The candidates picked for the review lead to its tab.
    expect(page.get_by_text("AI 검토에 고른 후보 1개")).to_be_visible()
    page.get_by_role("button", name="AI 검토로 이동").click()
    expect(page.get_by_role("tab", name="AI 검토")).to_have_attribute("aria-selected", "true")
    # Nothing can be sent yet, so the provider block is open; the summary line shows the state.
    expect(page.locator(".ai-dest-ready")).to_have_text("키 없음")
    page.get_by_label("AI 제공자").select_option(label="E2E 가짜 AI")
    page.get_by_label("모델 ID").fill("e2e-model")
    page.get_by_label(re.compile("E2E 가짜 AI API 키")).fill("test-key")
    page.get_by_role("button", name="키 저장").click()
    expect(page.get_by_label(re.compile("E2E 가짜 AI API 키 \\(있음\\)"))).to_be_visible()
    expect(page.locator(".ai-dest-ready")).to_have_text("키 있음")

    page.get_by_role("button", name="보낼 내용 미리보기").click()
    preview = page.locator(".ai-preview")
    expect(preview).to_contain_text(re.compile(r"요청 \d+회, 입력 추정 약 [\d,]+토큰"))
    preview.get_by_text("보낼 내용 전체").click()
    expect(preview.locator("pre")).to_contain_text('"instructions"')
    expect(preview.locator("pre")).to_contain_text('"chat:')

    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    expect(page.locator(".ai-status")).to_contain_text("AI 완료", timeout=10_000)
    expect(page.locator(".ai-done")).to_contain_text("각 후보 아래 ‘AI 설명’")
    page.locator(".ai-done").get_by_role("button", name="후보 보기").click()
    expect(page.get_by_role("tab", name=re.compile("^후보"))).to_have_attribute("aria-selected", "true")
    # The explanation sits under the candidate it was asked about, not in a row of its own:
    # one line until the row is chosen, then in full.
    first = page.locator("ol.candidates > li").first
    expect(first.locator(".ai-brief", has_text="AI 설명").first).to_be_visible()
    select_row(first)
    expect(first.locator(".ai-note")).to_contain_text("AI 설명")
    expect(first.locator(".ai-note")).to_contain_text("가짜 AI 추천")
    expect(page.locator("ol.candidates > li[data-source='ai']")).to_have_count(0)
    page.get_by_role("tab", name="파일").click()
    expect(page.get_by_role("button", name="recommendations.txt")).to_be_visible()


def test_comments_only_then_chat_and_hide_restore(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    page.get_by_label("치지직 다시보기 주소").fill("https://live.e2e.test/video/8")
    page.get_by_role("button", name="댓글만 수집").click()
    # The screen follows the job to its saved work.
    expect(page).to_have_url(re.compile(r"/sessions/[0-9a-f]{32}$"), timeout=20_000)
    page.wait_for_selector(READY, timeout=10_000)
    expect(page.get_by_text("영상 댓글만 받은 작업입니다")).to_be_visible(timeout=20_000)
    expect(page.locator("ol.candidates > li[data-source='viewer_comment']")).to_have_count(2)
    page.get_by_role("button", name="채팅 수집").click()
    expect(page.locator("ol.candidates > li[data-source='rule']").first).to_be_visible(timeout=30_000)
    expect(page.get_by_text("영상 댓글만 받은 작업입니다")).to_have_count(0)

    # Hiding and showing the list is tucked under 목록 정리 in the sidebar.
    page.get_by_text("목록 정리").click()
    page.get_by_role("button", name="목록에서 모두 숨기기").click()
    expect(page.get_by_text(re.compile(r"\d+개 작업을 목록에서 숨겼습니다"))).to_be_visible()
    expect(page.locator(".session-row")).to_have_count(0)
    page.get_by_role("button", name="숨긴 작업 다시 표시").click()
    expect(page.get_by_text(re.compile(r"\d+개 작업을 다시 표시합니다"))).to_be_visible()
    expect(page.locator(".session-row", has=page.get_by_role("link", name="E2E 가짜 다시보기 8"))).to_have_count(1)

"""Phase 4: viewer recommendations from comments, the unified timeline, the
opt-in AI review and hiding the saved list. Uses the E2E binary's synthetic
VOD (https://live.e2e.test/video/{id}) and synthetic AI provider, so nothing
leaves the machine. The helpers here choose the provider AI work sends to
(설정 > AI 검토) and turn the automatic review on or off (설정 > 일반 설정)."""
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


def save_defaults(page: Page) -> None:
    page.get_by_role("button", name="기본값 저장").click()
    expect(page.get_by_role("status").filter(has_text="기본값을 저장했습니다")).to_be_visible()


def open_ai_settings(page: Page, base_url: str) -> None:
    page.goto(base_url + "/settings")
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="AI 검토").click()


def use_provider(page: Page, base_url: str, provider: str, key: bool = True) -> None:
    """Makes provider the one AI work sends to (설정 > AI 검토), saving a test
    key first when key and it has none. Claude never gets a key in E2E, so
    nothing can reach Anthropic."""
    open_ai_settings(page, base_url)
    page.get_by_role("radio", name=provider, exact=True).check()
    card = page.get_by_role("region", name=provider, exact=True)
    missing = card.get_by_label(re.compile(f"^{re.escape(provider)} API 키 \\(없음\\)"))
    if key and missing.count():
        missing.fill("test-key")
        card.get_by_role("button", name="키 저장").click()
        expect(card.get_by_label(re.compile(f"^{re.escape(provider)} API 키 \\(있음\\)"))).to_be_visible()
    make = card.get_by_role("button", name="기본 제공자로 쓰기")
    if make.count():
        make.click()
        expect(card.locator(".provider-default")).to_contain_text("기본 제공자입니다")
        save_defaults(page)


def use_claude(page: Page, base_url: str) -> None:
    """Back to the built-in default provider (without a key) for later tests."""
    use_provider(page, base_url, "Claude (Anthropic)", key=False)


def set_auto_ai(page: Page, base_url: str, on: bool) -> None:
    """Turns 일반 설정's automatic AI review on (agreeing to what it sends) or off, and saves."""
    page.goto(base_url + "/settings")
    page.wait_for_selector(READY, timeout=10_000)
    box = page.get_by_role("checkbox", name=re.compile("^AI 검토 자동 실행"))
    if box.is_checked() == on:
        return
    box.click()
    if on:
        page.get_by_role("group", name="AI 검토 자동 실행 확인").get_by_role("button", name="동의하고 켜기").click()
        expect(box).to_be_checked()
    else:
        expect(box).not_to_be_checked()
    save_defaults(page)


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


def test_ai_review_from_the_candidate_list(page: Page, base_url: str) -> None:
    """With the automatic review off (the default), the AI line above the
    candidates sends one session's scene explanations on request, asked once
    more; the transcript the collection made by itself is fixed first and
    read with the chat. Explanations sit under the candidates they explain."""
    use_provider(page, base_url, "E2E 가짜 AI")
    try:
        open_video(page, base_url, "E2E 가짜 다시보기 7", VIDEO)
        bar = page.locator(".ai-bar")
        expect(bar).to_contain_text("AI 장면 설명은 자동으로 받지 않습니다.")
        expect(page.get_by_role("tab", name="AI 검토")).to_have_count(0)
        # The collection transcribed the broadcast by itself (일반 설정, on by default); the button waits for it.
        bar.get_by_role("button", name="AI 장면 설명 받기").click(timeout=30_000)
        question = page.get_by_role("group", name="AI 장면 설명 받기 확인")
        expect(question).to_contain_text("E2E 가짜 AI로 보내")
        expect(question).to_contain_text("요청 60회")
        question.get_by_role("button", name="AI 장면 설명 받기").click()
        job = page.locator(".job")
        expect(job).to_contain_text("AI 검토가 끝났습니다.", timeout=30_000)
        expect(job).to_contain_text(re.compile(r"AI 대본 다듬기 \d+줄, AI 장면 설명 \d+개까지 마쳤습니다"))

        # The explanation sits under the candidate it was asked about, not in a row of its own:
        # one line until the row is chosen, then in full.
        first = page.locator("ol.candidates > li").first
        expect(first.locator(".ai-brief", has_text="AI 설명").first).to_be_visible()
        select_row(first)
        expect(first.locator(".ai-note")).to_contain_text("AI 설명")
        expect(first.locator(".ai-note")).to_contain_text("가짜 AI 추천")
        expect(page.locator("ol.candidates > li[data-source='ai']")).to_have_count(0)
        page.screenshot(path=str(SHOTS / "ai-explained.png"), full_page=True)

        # The fixed transcript keeps what was heard beside each fix.
        page.get_by_role("tab", name="대본").click()
        expect(page.locator(".transcript-fixed")).to_contain_text("줄을 다듬었습니다")
        lines = page.get_by_role("list", name="발언 목록")
        expect(lines.locator(".chat-fixed").first).to_be_visible()
        expect(lines.locator("li").first).to_contain_text("가짜 발화")
        page.get_by_role("tab", name="파일").click()
        expect(page.get_by_role("button", name="recommendations.txt")).to_be_visible()
    finally:
        use_claude(page, base_url)


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

"""Phase 3: LIVE capture against the synthetic platform of the E2E binary
(build tag e2efake, https://live.e2e.test/{channel}). Its broadcast sends 20
minutes of chat on connect with a laugh burst, then one message per 500 ms,
and its audio as segments on the same clock. Candidates come in tiers
(REDESIGN 6.2.3): early at once, judged once five minutes of chat followed
the burst (at once here), final when the capture stops."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'
LIVE_URL = "https://live.e2e.test/alpha"


def open_home(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)


def select_row(row) -> None:
    """AI results open in full only under the chosen row: choose it (pressing a chosen row closes it)."""
    head = row.locator(".candidate-head")
    if head.get_attribute("aria-pressed") != "true":
        head.click()
    expect(head).to_have_attribute("aria-pressed", "true")


def stop_capture(page: Page) -> None:
    """Stopping is asked once more (the chat while stopped is lost): the button, then the same action in its question."""
    page.get_by_role("button", name="수집 중단").click()
    page.get_by_role("group", name="수집 중단 확인").get_by_role("button", name="수집 중단").click()


def live_form(page: Page):
    page.get_by_role("tab", name="생방송").click()
    return page.get_by_role("form", name="생방송 수집")


def test_live_capture_review_save_stop_resume(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    form = live_form(page)
    form.get_by_label("치지직 생방송 주소").fill(LIVE_URL)
    form.get_by_role("button", name="방송 확인").click()
    expect(form.get_by_role("status")).to_contain_text("방송 중, 시청자 1,234명")
    form.get_by_role("button", name="수집 시작").click()

    expect(page).to_have_url(re.compile(r"/live/[0-9a-f]{32}$"))
    page.wait_for_selector(READY, timeout=10_000)
    expect(page.get_by_role("heading", level=1, name="E2E 가짜 생방송 alpha")).to_be_visible()
    expect(page.locator(".live-state")).to_have_text("채팅 수신 중", timeout=10_000)
    log = page.get_by_role("list", name="실시간 채팅")
    expect(log.locator("li").first).to_be_visible()

    # The broadcast's stream (silent audio from the fake) loads only once played.
    screen = page.locator(".stage-video")
    expect(screen.get_by_role("heading", name="영상")).to_be_visible()
    expect(screen.get_by_text("재생하면 영상을 받기 시작합니다.", exact=False)).to_be_visible()
    with page.expect_request(re.compile(r"/live/alpha/\d+\.aac$")):
        page.get_by_label("생방송 영상").evaluate("v => { v.muted = true; return v.play(); }")
    page.wait_for_function("() => document.querySelector('video[aria-label=\"생방송 영상\"]').currentTime > 1", timeout=10_000)
    expect(screen.get_by_text("재생하면 영상을 받기 시작합니다.", exact=False)).to_be_hidden()
    expect(screen.get_by_role("alert")).to_have_count(0)

    # The laugh burst is judged once five minutes of chat followed it; the early candidate gives way to it.
    expect(page.locator(".tier-legend")).to_contain_text("5분 판정")
    cand = page.locator("ol.candidates > li").first
    expect(cand.locator(".candidate-tier")).to_have_text("5분 판정", timeout=20_000)
    expect(page.get_by_role("heading", name="편집 후보 1개")).to_be_visible()
    expect(cand).to_contain_text("웃음 증가")
    expect(cand.locator(".candidate-strength")).to_be_visible()  # judged like a VOD: strength and rank
    timeline = page.get_by_role("img", name=re.compile("반응 밀도 그래프"))
    expect(timeline).to_have_attribute("aria-label", re.compile("채팅을 받지 못한 구간 1개"))  # before the capture started
    cand.get_by_role("button").first.click()
    expect(cand.locator(".evidence li").first).to_contain_text("ㅋㅋㅋㅋㅋ")
    expect(cand.get_by_role("list", name="편집 구간 채팅").locator("li[data-evidence]").first).to_be_visible()
    # Its range was recognised from the audio recorded meanwhile (일반 설정, on by default).
    speech = cand.get_by_role("list", name="편집 구간 발언")
    expect(speech.locator("li").first).to_contain_text("방송자", timeout=20_000)
    expect(cand).to_contain_text(re.compile(r"편집 구간 방송 내용 \d+줄 \(이 컴퓨터에서 인식\)"))
    expect(page.locator(".timeline-gap")).to_have_count(1)
    # The capture joined the broadcast late: the timeline starts where the chat does, the whole broadcast a click away.
    expect(page.get_by_role("radio", name="수집한 구간")).to_be_checked()
    expect(timeline).to_have_attribute("aria-label", re.compile("확대해 보는 중"))
    page.screenshot(path=str(SHOTS / "live-running.png"), full_page=True)

    # New chat keeps arriving over SSE.
    before = log.locator("li").last.inner_text()
    expect(log.locator("li").last).not_to_have_text(before, timeout=5_000)

    page.get_by_role("button", name="현재까지 저장").click()
    expect(page.get_by_text(re.compile(r"채팅 [\d,]+건과 편집 후보를 저장했습니다"))).to_be_visible()
    page.get_by_role("tab", name="파일").click()
    with page.expect_download() as info:
        page.get_by_role("button", name="편집 후보 TXT").click()
    text = Path(info.value.path()).read_text(encoding="utf-8")
    assert text.startswith("MomentForge 편집 후보") and "방송 시작 후 경과" in text, text[:200]

    # Stopping is asked once more; keeping on leaves the capture running.
    page.get_by_role("button", name="수집 중단").click()
    question = page.get_by_role("group", name="수집 중단 확인")
    expect(question).to_contain_text("멈춘 동안의 채팅은 받을 수 없습니다")
    question.get_by_role("button", name="계속 수집").click()
    expect(question).to_have_count(0)
    expect(page.locator(".live-state")).to_have_text("채팅 수신 중")
    stop_capture(page)
    expect(page.locator(".live-state")).to_have_text("중단됨", timeout=10_000)
    expect(page.get_by_text("수집을 중단했습니다.")).to_be_visible()
    expect(cand.locator(".candidate-tier")).to_have_text("확정")  # stopping settles what was judged
    expect(page.get_by_role("button", name="현재까지 저장")).to_have_count(0)  # a stopped capture has saved everything
    expect(screen.get_by_label("생방송 영상")).to_have_count(0)
    expect(screen).to_contain_text("생방송 영상은 수집하는 동안에만 보여 줍니다. 이어 기록하면 다시 불러옵니다.")
    # Switched off, the video card is one line and the chat takes the room.
    assert screen.bounding_box()["height"] < 120, screen.bounding_box()
    page.get_by_role("button", name="이어 기록").click()
    expect(page.locator(".live-state")).to_have_text("채팅 수신 중", timeout=10_000)
    page.get_by_role("tab", name="수집 상태").click()
    expect(page.locator(".facts")).to_contain_text("반응 5분 뒤 다시보기와 같은 기준")
    expect(page.locator(".facts")).to_contain_text("받는 중 (가장 낮은 화질, 최근 25분만")
    expect(page.locator(".facts")).to_contain_text("연결 복구 1회")
    expect(page.locator(".facts")).to_contain_text("2개")  # the stop became a second gap
    stop_capture(page)
    expect(page.locator(".live-state")).to_have_text("중단됨", timeout=10_000)

    # The saved list follows the capture's state.
    row = page.locator(".session-row", has=page.get_by_role("link", name="E2E 가짜 생방송 alpha"))
    expect(row).to_contain_text("생방송 중단됨")
    page.get_by_role("link", name="새 작업").click()
    page.wait_for_selector(READY, timeout=10_000)
    row.get_by_role("link").click()
    expect(page).to_have_url(re.compile(r"/live/[0-9a-f]{32}$"))


def test_live_rejects_offline_and_wrong_form(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    form = live_form(page)
    form.get_by_label("치지직 생방송 주소").fill("https://live.e2e.test/offline")
    form.get_by_role("button", name="수집 시작").click()
    expect(form.get_by_role("alert")).to_have_text("현재 공개 채팅을 받을 수 있는 생방송이 아닙니다.")

    page.get_by_role("tab", name="다시보기").click()
    page.get_by_label("치지직 다시보기 주소").fill(LIVE_URL)
    page.get_by_role("button", name="수집하고 편집점 찾기").click()
    expect(page.get_by_text("생방송 주소입니다. 생방송 수집 칸에서 시작하세요.")).to_be_visible()


test_live_rejects_offline_and_wrong_form.allow_console = ["status of 400"]


def test_live_review_by_ai_while_capturing(page: Page, base_url: str) -> None:
    """AI review of a running capture (REDESIGN 6.2.3, 15): only judged
    candidates go; the AI line sends them on request, with the speech
    recognised in their range, and a judged candidate offers its cut analysis."""
    from test_phase4 import use_claude, use_provider

    use_provider(page, base_url, "E2E 가짜 AI")
    try:
        open_home(page, base_url)
        row = page.locator(".session-row", has=page.get_by_role("link", name="E2E 가짜 생방송 alpha"))
        if row.count() == 0:  # running this test alone: make a capture first
            live_form(page).get_by_label("치지직 생방송 주소").fill(LIVE_URL)
            live_form(page).get_by_role("button", name="수집 시작").click()
        else:
            row.get_by_role("link").click()
        page.wait_for_selector(READY, timeout=10_000)
        if page.get_by_role("button", name="이어 기록").count():
            page.get_by_role("button", name="이어 기록").click()
        expect(page.locator(".live-state")).to_have_text("채팅 수신 중", timeout=10_000)
        judged = page.locator("ol.candidates > li", has=page.locator(".candidate-tier:not([data-tier='early'])"))
        expect(judged.first).to_be_visible(timeout=20_000)

        bar = page.locator(".ai-bar")
        expect(bar).to_contain_text("AI 장면 설명은 자동으로 받지 않습니다.")
        bar.get_by_role("button", name="AI 장면 설명 받기").click()
        question = page.get_by_role("group", name="AI 장면 설명 받기 확인")
        expect(question).to_contain_text("E2E 가짜 AI로 보내")
        question.get_by_role("button", name="AI 장면 설명 받기").click()

        # The synthetic provider explains the candidate it was sent: the answer sits under that candidate,
        # in full once the row is chosen.
        select_row(judged.first)
        note = judged.first.locator(".ai-note")
        expect(note).to_contain_text("가짜 AI 추천", timeout=15_000)
        expect(note).to_contain_text("AI 설명")
        expect(page.locator("ol.candidates > li[data-source='ai']")).to_have_count(0)
        expect(judged.first.locator(".cut-start").get_by_role("button", name="AI 컷 분석")).to_be_visible()
        # The summary counts the rows, as the list does.
        shown = page.locator("ol.candidates > li").count()
        expect(page.locator(".stat", has_text="편집 후보").locator("dd")).to_have_text(f"{shown}개")
        page.wait_for_timeout(100)  # a shown tab replays its rows' entrance; let the frame settle for the picture
        page.screenshot(path=str(SHOTS / "live-ai.png"), full_page=True)
        page.get_by_role("tab", name="파일").click()
        expect(page.get_by_role("button", name="AI 추천 TXT")).to_be_visible()

        stop_capture(page)
        expect(page.locator(".live-state")).to_have_text("중단됨", timeout=10_000)
    finally:
        use_claude(page, base_url)


def test_live_view_dark_and_mobile(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    row = page.locator(".session-row", has=page.get_by_role("link", name="E2E 가짜 생방송 alpha"))
    if row.count() == 0:  # running this test alone: make a capture first
        live_form(page).get_by_label("치지직 생방송 주소").fill(LIVE_URL)
        live_form(page).get_by_role("button", name="수집 시작").click()
    else:
        row.get_by_role("link").click()
    expect(page).to_have_url(re.compile(r"/live/[0-9a-f]{32}$"))
    page.wait_for_selector(READY, timeout=10_000)
    page.emulate_media(color_scheme="dark")
    page.set_viewport_size({"width": 375, "height": 812})
    expect(page.get_by_role("heading", name=re.compile(r"편집 후보 \d+개"))).to_be_visible()
    overflow = page.evaluate("document.documentElement.scrollWidth - document.documentElement.clientWidth")
    assert overflow <= 0, f"horizontal overflow {overflow}px"
    page.screenshot(path=str(SHOTS / "live-mobile-dark.png"), full_page=True)
    if page.get_by_role("button", name="수집 중단").count():
        stop_capture(page)
        expect(page.locator(".live-state")).to_have_text("중단됨", timeout=10_000)


def test_live_without_stream_keeps_chat_and_platform_link(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    form = live_form(page)
    form.get_by_label("치지직 생방송 주소").fill("https://live.e2e.test/novideo")
    form.get_by_role("button", name="수집 시작").click()
    expect(page).to_have_url(re.compile(r"/live/[0-9a-f]{32}$"))
    page.wait_for_selector(READY, timeout=10_000)
    expect(page.locator(".live-state")).to_have_text("채팅 수신 중", timeout=10_000)
    screen = page.locator(".stage-video")
    expect(screen.get_by_role("alert")).to_contain_text("방송 영상 주소를 확인하지 못했습니다.")
    expect(screen.get_by_role("button", name="다시 불러오기")).to_be_visible()
    expect(page.get_by_role("link", name="E2E 가짜 방송에서 열기")).to_be_visible()
    expect(page.get_by_role("list", name="실시간 채팅").locator("li").first).to_be_visible()
    # Without a stream there is no audio to recognise either; the capture state says why.
    page.get_by_role("tab", name="수집 상태").click()
    expect(page.locator(".facts")).to_contain_text("방송 영상 주소를 확인하지 못했습니다.")
    page.screenshot(path=str(SHOTS / "live-no-stream.png"), full_page=True)
    stop_capture(page)
    expect(page.locator(".live-state")).to_have_text("중단됨", timeout=10_000)


test_live_without_stream_keeps_chat_and_platform_link.allow_console = ["status of 400"]

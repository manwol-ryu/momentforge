"""Phase 3: LIVE capture against the synthetic platform of the E2E binary
(build tag e2efake, https://live.e2e.test/{channel}). Its broadcast sends 20
minutes of chat on connect with a laugh burst, then one message per 500 ms."""
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

    # The laugh burst becomes a confirmed candidate once the chat after it arrives.
    expect(page.get_by_role("heading", name="편집 후보 1개")).to_be_visible(timeout=20_000)
    cand = page.locator("ol.candidates > li").first
    expect(cand).to_contain_text("웃음 증가")
    expect(cand.locator(".candidate-state")).to_have_text("확정")
    timeline = page.get_by_role("img", name=re.compile("반응 밀도 그래프"))
    expect(timeline).to_have_attribute("aria-label", re.compile("채팅을 받지 못한 구간 1개"))  # before the capture started
    cand.get_by_role("button").first.click()
    expect(cand.locator(".evidence li").first).to_contain_text("ㅋㅋㅋㅋㅋ")
    expect(cand.get_by_role("list", name="편집 구간 채팅").locator("li[data-evidence]").first).to_be_visible()
    expect(page.locator(".timeline-gap")).to_have_count(1)
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

    page.get_by_role("button", name="수집 중단").click()
    expect(page.locator(".live-state")).to_have_text("중단됨", timeout=10_000)
    expect(page.get_by_text("수집을 중단했습니다.")).to_be_visible()
    page.get_by_role("button", name="이어 기록").click()
    expect(page.locator(".live-state")).to_have_text("채팅 수신 중", timeout=10_000)
    page.get_by_role("tab", name="수집 상태").click()
    expect(page.locator(".facts")).to_contain_text("연결 복구 1회")
    expect(page.locator(".facts")).to_contain_text("2개")  # the stop became a second gap
    page.get_by_role("button", name="수집 중단").click()
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
        page.get_by_role("button", name="수집 중단").click()
        expect(page.locator(".live-state")).to_have_text("중단됨", timeout=10_000)

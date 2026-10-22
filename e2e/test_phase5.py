"""Phase 5 and the automatic review (REDESIGN 15, 2026-10-22): Claude as the
default provider with a model per task, the automatic AI review that runs
once a collection ends (turned on after agreeing to what it sends), and the
providers that sign in through their own programs. Uses the E2E binary's
synthetic VOD and AI providers, so nothing leaves the machine (the Claude
provider is only shown, never sent to)."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

from test_phase4 import open_ai_settings, open_video, save_defaults, set_auto_ai, use_claude, use_provider

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'
VIDEO = "https://live.e2e.test/video/9"


def test_claude_is_the_default_provider(page: Page, base_url: str) -> None:
    open_ai_settings(page, base_url)
    card = page.get_by_role("region", name="Claude (Anthropic)", exact=True)
    expect(card.locator(".provider-default")).to_contain_text("기본 제공자입니다")
    # Each task has the model that fits it unless one is set: fast for the transcript, the strongest for cuts.
    expect(card.get_by_label("대본 다듬기 모델")).to_have_attribute("placeholder", "claude-haiku-5-5")
    expect(card.get_by_label("장면 설명 모델")).to_have_attribute("placeholder", "claude-sonnet-5-5")
    expect(card.get_by_label("컷 분석 모델")).to_have_attribute("placeholder", "claude-opus-5-5")
    card.screenshot(path=str(SHOTS / "settings-task-models.png"))


def test_auto_review_explains_once_a_collection_ends(page: Page, base_url: str) -> None:
    """Turned on in 일반 설정, a collected VOD is transcribed, its transcript
    fixed and its candidates explained without asking, within the session's
    limits; the AI line says it runs by itself."""
    use_provider(page, base_url, "E2E 가짜 AI")
    try:
        set_auto_ai(page, base_url, True)
        open_video(page, base_url, "E2E 가짜 다시보기 9", VIDEO)
        job = page.locator(".job")
        expect(job).to_contain_text(re.compile(r"방송 내용 인식, AI 대본 다듬기 \d+줄, AI 장면 설명 \d+개까지 마쳤습니다"),
                                    timeout=30_000)
        bar = page.locator(".ai-bar")
        expect(bar).to_contain_text("AI 검토 자동 실행 중.")
        expect(bar).to_contain_text("E2E 가짜 AI(e2e-model)")
        expect(bar.get_by_role("button", name="AI 장면 설명 받기")).to_have_count(0)
        first = page.locator("ol.candidates > li[data-source='rule']").first
        expect(first.locator(".ai-brief", has_text="AI 설명").first).to_be_visible()
        page.screenshot(path=str(SHOTS / "ai-auto.png"), full_page=True)
    finally:
        set_auto_ai(page, base_url, False)
        use_claude(page, base_url)


def test_opencode_uses_its_own_login(page: Page, base_url: str) -> None:
    """OpenCode signs in through the OpenCode CLI: no key field. The E2E
    binary points it at a missing CLI, so its card explains how to set up."""
    open_ai_settings(page, base_url)
    page.get_by_role("radio", name="OpenCode", exact=True).check()
    card = page.get_by_role("region", name="OpenCode", exact=True)
    expect(card.locator(".ai-auth")).to_contain_text("OpenCode CLI(opencode)를 찾지 못했습니다")
    expect(card.get_by_text("키는 MomentForge에 넣지 않습니다", exact=False)).to_be_visible()
    expect(card.get_by_role("button", name="키 저장")).to_have_count(0)
    expect(card.locator("input[type=password]")).to_have_count(0)
    card.screenshot(path=str(SHOTS / "ai-opencode.png"))


def test_cliproxy_sends_to_the_address_in_settings(page: Page, base_url: str) -> None:
    """CLIProxyAPI: the address, key and model are set in 설정 and a review
    goes there. Nothing listens at the address, so the review stops with the
    address in the job's warning and nothing leaves."""
    open_ai_settings(page, base_url)
    page.get_by_role("radio", name="CLIProxyAPI").check()  # its card shows once it is chosen
    card = page.locator("section.provider", has=page.get_by_role("heading", name="CLIProxyAPI"))
    # Its running state and switch: the E2E binary has no cli-proxy-api, so it cannot be turned on.
    service = card.get_by_role("group", name="CLIProxyAPI 실행 상태")
    expect(service.get_by_role("status")).to_have_text("설치되지 않음")
    expect(service.get_by_role("button", name="CLIProxyAPI 켜기")).to_be_disabled()
    expect(service).to_contain_text("찾지 못함")
    address = card.get_by_label("CLIProxyAPI 주소")
    expect(address).to_have_attribute("placeholder", "http://127.0.0.1:1/v1")  # the E2E binary's own, never a real CLIProxyAPI
    address.fill("http://127.0.0.1:9/v1")
    card.get_by_label("기본 모델 ID").fill("test-model")
    save_defaults(page)
    card.get_by_label(re.compile("^CLIProxyAPI API 키")).fill("test-key")
    card.get_by_role("button", name="키 저장").click()
    expect(card.locator(".provider-state")).to_have_text("키 있음")
    card.screenshot(path=str(SHOTS / "settings-cliproxy.png"))
    # Setting it up did not make it the default; making it so is its own step.
    expect(card.get_by_role("button", name="기본 제공자로 쓰기")).to_be_visible()
    try:
        use_provider(page, base_url, "CLIProxyAPI")
        page.get_by_role("link", name="새 작업").click()
        page.wait_for_selector(READY, timeout=10_000)
        page.get_by_role("button", name="샘플로 먼저 체험").click()
        page.wait_for_selector(READY, timeout=10_000)
        bar = page.locator(".ai-bar")
        bar.get_by_role("button", name="AI 장면 설명 받기").click()
        question = page.get_by_role("group", name="AI 장면 설명 받기 확인")
        expect(question).to_contain_text("CLIProxyAPI로 보내")
        question.get_by_role("button", name="AI 장면 설명 받기").click()
        expect(page.locator(".job")).to_contain_text("CLIProxyAPI(http://127.0.0.1:9/v1)에 연결하지 못했습니다", timeout=15_000)
    finally:
        # Back to the default address and provider for the rest of the run.
        use_claude(page, base_url)
        page.get_by_role("radio", name="CLIProxyAPI").check()
        card = page.locator("section.provider", has=page.get_by_role("heading", name="CLIProxyAPI"))
        card.get_by_label("CLIProxyAPI 주소").fill("")
        card.get_by_label("기본 모델 ID").fill("")
        save_defaults(page)


def test_opencodex_needs_no_key_and_is_switched_in_settings(page: Page, base_url: str) -> None:
    """OpenCodex: no key field; its card shows whether it runs and the switch.
    The E2E binary points it at a missing ocx, so it cannot be turned on."""
    open_ai_settings(page, base_url)
    page.get_by_role("radio", name="OpenCodex", exact=True).check()
    card = page.locator("section.provider", has=page.get_by_role("heading", name="OpenCodex", exact=True))
    service = card.get_by_role("group", name="OpenCodex 실행 상태")
    expect(service.get_by_role("status")).to_have_text("설치되지 않음")
    expect(service.get_by_role("button", name="OpenCodex 켜기")).to_be_disabled()
    expect(service).to_contain_text("README의 OpenCodex 준비 방법")
    expect(service.get_by_role("link", name="OpenCodex 대시보드 열기")).to_have_count(0)
    expect(card.get_by_label("OpenCodex 주소")).to_have_attribute("placeholder", "http://127.0.0.1:1/v1")
    expect(card).to_contain_text("키는 필요 없습니다")
    expect(card.locator(".provider-state")).to_have_text("준비 안 됨")
    expect(card.locator(".ai-auth")).to_contain_text("OpenCodex(ocx)를 찾지 못했습니다")
    expect(card.locator("input[type=password]")).to_have_count(0)
    card.screenshot(path=str(SHOTS / "settings-opencodex.png"))
    # CLIProxyAPI's card keeps its own address text.
    page.get_by_role("radio", name="CLIProxyAPI").check()
    expect(page.locator("section.provider")).to_contain_text("access.api-keys")


def test_codex_uses_its_own_login(page: Page, base_url: str) -> None:
    """Codex signs in through the Codex CLI (ChatGPT): no key field. The E2E
    binary points it at a missing CLI, so its card explains how to set up."""
    open_ai_settings(page, base_url)
    page.get_by_role("radio", name="Codex", exact=True).check()
    card = page.get_by_role("region", name="Codex", exact=True)
    expect(card.locator(".ai-auth")).to_contain_text("Codex CLI(codex)를 찾지 못했습니다")
    expect(card.locator("input[type=password]")).to_have_count(0)

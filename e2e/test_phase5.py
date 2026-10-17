"""Phase 5: Claude as the default provider, the cost preview, exact token
counting, batch review under a cost cap, and comparing two providers'
reviews. Uses the E2E binary's synthetic VOD and AI providers, so nothing
leaves the machine (the Claude provider is only shown, never sent to)."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

from test_phase4 import open_home, open_video

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'
VIDEO = "https://live.e2e.test/video/9"


def test_claude_is_the_default_provider(page: Page, base_url: str) -> None:
    open_home(page, base_url)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="AI 검토").click()
    provider = page.get_by_label("AI 제공자")
    expect(provider.locator("option:checked")).to_have_text("Claude (Anthropic)")
    expect(page.get_by_label("모델 ID")).to_have_value("claude-opus-5-5")
    expect(page.get_by_label("배치로 보내기", exact=False)).to_be_visible()
    expect(page.get_by_role("textbox", name=re.compile("^비용 한도"))).to_be_visible()
    expect(page.get_by_label("모든 후보 자동 선택", exact=False)).to_be_visible()


def save_key(page: Page, provider: str) -> None:
    page.get_by_label("AI 제공자").select_option(label=provider)
    page.get_by_label(re.compile(f"^{re.escape(provider)} API 키")).fill("test-key")
    page.get_by_role("button", name="키 저장").click()
    expect(page.get_by_label(re.compile(f"^{re.escape(provider)} API 키 \\(메모리에 있음\\)"))).to_be_visible()


def test_batch_review_with_cost_cap_then_compare(page: Page, base_url: str) -> None:
    open_video(page, base_url, "E2E 가짜 다시보기 9", VIDEO)
    page.get_by_role("tab", name="AI 검토").click()

    # Every candidate, as one batch, within a cost cap.
    save_key(page, "E2E 가짜 AI")
    page.get_by_role("button", name="쓸 수 있는 모델 불러오기").click()
    expect(page.get_by_role("button", name="모델 2개 불러옴")).to_be_visible()
    page.get_by_label("모든 후보 자동 선택", exact=False).check()
    expect(page.get_by_role("button", name="보낼 내용 미리보기")).to_be_disabled()  # a cap is required
    page.get_by_label("배치로 보내기", exact=False).check()
    page.get_by_role("textbox", name=re.compile("^비용 한도")).fill("1")
    page.get_by_role("button", name="보낼 내용 미리보기").click()
    preview = page.locator(".ai-preview")
    expect(preview).to_contain_text("한 번에 배치로 보냅니다")
    expect(preview.locator(".ai-cost").first).to_contain_text(re.compile(r"예상 비용 최대 \$[\d.]+ \(한도 \$1\.00\)"))
    expect(preview.locator(".ai-cost").first).to_contain_text("배치 50% 할인 적용")
    page.get_by_role("button", name="정확한 토큰 수 계산").click()
    expect(preview.locator(".ai-cost").nth(1)).to_contain_text(re.compile(r"정확한 입력 토큰 [\d,]+개, 예상 비용 최대 \$"))
    page.locator("section", has=page.get_by_role("heading", name="선택형 AI 검토")).screenshot(path=str(SHOTS / "ai-panel.png"))
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    status = page.locator(".ai-status")
    expect(status).to_contain_text("AI 완료", timeout=15_000)
    expect(status).to_contain_text(re.compile(r"캐시 적중 [\d,]+"))
    expect(status).to_contain_text(re.compile(r"비용 \$[\d.]+"))

    # The same candidates by the second provider, one request at a time.
    save_key(page, "E2E 가짜 AI 2")
    page.get_by_label("모든 후보 자동 선택", exact=False).wait_for(state="detached")  # no prices: no automatic selection
    page.get_by_role("tab", name=re.compile("^후보")).click()
    rule_items = page.locator("ol.candidates > li:not([data-source='ai'])")
    for i in range(rule_items.count()):
        rule_items.nth(i).get_by_label("AI 검토에 포함").check()
    page.get_by_role("button", name="AI 검토로 이동").click()
    page.get_by_role("button", name="보낼 내용 미리보기").click()
    expect(page.locator(".ai-preview")).to_contain_text("이 모델의 가격 정보가 없어")
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    expect(status).to_contain_text("AI 완료", timeout=15_000)

    compare = page.locator("section", has=page.get_by_role("heading", name="AI 결과 비교"))
    expect(compare).to_be_visible()
    expect(compare.get_by_role("status")).to_contain_text(re.compile(r"겹치는 추천 \d+개, A에만 \d+개, B에만 \d+개"))
    expect(compare.locator(".compare-segment").first).to_be_attached()
    expect(compare.get_by_role("table")).to_contain_text("E2E 가짜 AI")
    compare.scroll_into_view_if_needed()
    compare.screenshot(path=str(SHOTS / "ai-compare.png"))
    page.set_viewport_size({"width": 390, "height": 900})
    compare.scroll_into_view_if_needed()
    compare.screenshot(path=str(SHOTS / "ai-compare-mobile.png"))


def test_opencode_uses_its_own_login(page: Page, base_url: str) -> None:
    """OpenCode signs in through the OpenCode CLI: no key field. The E2E
    binary points it at a missing CLI, so the screen explains how to set up.
    Free models (opencode/…-free) are offered and previewed like Go models."""
    open_home(page, base_url)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="AI 검토").click()
    page.get_by_label("AI 제공자").select_option(label="OpenCode")
    expect(page.get_by_label("모델 ID")).to_have_value(re.compile(r"^opencode-go/"))
    expect(page.locator(".ai-auth")).to_contain_text("OpenCode CLI(opencode)를 찾지 못했습니다")
    expect(page.get_by_text("키는 MomentForge에 넣지 않습니다", exact=False)).to_be_visible()
    expect(page.get_by_text("무료 모델(opencode/…-free)은 OpenCode 기본 에이전트로 보내고 도구는 모두 자동 거부", exact=False)).to_be_visible()
    expect(page.get_by_role("button", name="키 저장")).to_have_count(0)
    expect(page.locator("input[type=password]")).to_have_count(0)
    page.locator("section", has=page.get_by_role("heading", name="선택형 AI 검토")).screenshot(path=str(SHOTS / "ai-opencode.png"))

    # A free model is previewed like any other; without the CLI it cannot be sent yet.
    page.get_by_label("모델 ID").fill("opencode/deepseek-v4.1-flash-free")
    page.get_by_label("채팅 구간 직접 지정", exact=False).check()
    page.get_by_role("button", name="보낼 내용 미리보기").click()
    expect(page.locator(".ai-preview")).to_contain_text(re.compile(r"요청 \d+회"))
    expect(page.get_by_role("button", name="확인한 내용으로 AI 실행")).to_be_disabled()


def test_cliproxy_sends_to_the_address_in_settings(page: Page, base_url: str) -> None:
    """CLIProxyAPI: the address and key are set in 설정, the AI tab says
    where it sends, and a run goes there. Nothing listens at the address, so
    the run stops with the address in the message and nothing leaves."""
    page.goto(base_url + "/settings")
    page.wait_for_selector(READY, timeout=10_000)
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
    page.get_by_role("button", name="기본값 저장").click()
    expect(page.get_by_role("status").filter(has_text="기본값을 저장했습니다")).to_be_visible()
    card.get_by_label(re.compile("^CLIProxyAPI API 키")).fill("test-key")
    card.get_by_role("button", name="키 저장").click()
    expect(card.locator(".provider-state")).to_have_text("키 있음")
    card.screenshot(path=str(SHOTS / "settings-cliproxy.png"))

    page.get_by_role("link", name="새 작업").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="AI 검토").click()
    page.get_by_label("AI 제공자").select_option(label="CLIProxyAPI")
    expect(page.locator(".ai-endpoint")).to_contain_text("http://127.0.0.1:9/v1")
    page.get_by_label("모델 ID").fill("test-model")
    page.get_by_label("채팅 구간 직접 지정", exact=False).check()
    page.get_by_role("button", name="보낼 내용 미리보기").click()
    expect(page.locator(".ai-preview")).to_contain_text(re.compile(r"요청 \d+회"))
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    expect(page.locator(".ai-status")).to_contain_text("CLIProxyAPI(http://127.0.0.1:9/v1)에 연결하지 못했습니다", timeout=15_000)

    # Back to the default address and provider for the rest of the run.
    page.goto(base_url + "/settings")
    page.wait_for_selector(READY, timeout=10_000)
    page.locator("section.provider", has=page.get_by_role("heading", name="CLIProxyAPI")).get_by_label("CLIProxyAPI 주소").fill("")
    page.get_by_role("radio", name="Claude (Anthropic)").check()
    page.get_by_role("button", name="기본값 저장").click()
    expect(page.get_by_role("status").filter(has_text="기본값을 저장했습니다")).to_be_visible()


def test_opencodex_needs_no_key_and_is_switched_in_settings(page: Page, base_url: str) -> None:
    """OpenCodex: no key field; its card shows whether it runs and the switch.
    The E2E binary points it at a missing ocx, so it cannot be turned on, and
    the AI tab says where it would send."""
    page.goto(base_url + "/settings")
    page.wait_for_selector(READY, timeout=10_000)
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

    page.get_by_role("link", name="새 작업").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="AI 검토").click()
    page.get_by_label("AI 제공자").select_option(label="OpenCodex")
    expect(page.locator(".ai-endpoint")).to_contain_text("http://127.0.0.1:1/v1")
    expect(page.get_by_text("MomentForge에 키를 넣지 않습니다", exact=False)).to_be_visible()
    expect(page.locator("input[type=password]")).to_have_count(0)


def test_codex_uses_its_own_login(page: Page, base_url: str) -> None:
    """Codex signs in through the Codex CLI (ChatGPT): no key field. The E2E
    binary points it at a missing CLI, so the screen explains how to set up."""
    open_home(page, base_url)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="AI 검토").click()
    page.get_by_label("AI 제공자").select_option(label="Codex")
    expect(page.get_by_label("모델 ID")).to_have_value("default")
    expect(page.locator(".ai-auth")).to_contain_text("Codex CLI(codex)를 찾지 못했습니다")
    expect(page.get_by_text("셸·브라우저·컴퓨터 사용·앱·플러그인·이미지 생성·웹 검색 도구를 모두 끄고", exact=False)).to_be_visible()
    expect(page.locator("input[type=password]")).to_have_count(0)

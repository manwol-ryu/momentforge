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
    rule_items = page.locator("ol.candidates > li:not([data-source='ai'])")
    for i in range(rule_items.count()):
        rule_items.nth(i).get_by_label("AI 검토에 포함").check()
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


def test_opencode_go_uses_its_own_login(page: Page, base_url: str) -> None:
    """OpenCode Go signs in through the OpenCode CLI: no key field. The E2E
    binary points it at a missing CLI, so the screen explains how to set up."""
    open_home(page, base_url)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_label("AI 제공자").select_option(label="OpenCode Go")
    expect(page.get_by_label("모델 ID")).to_have_value(re.compile(r"^opencode-go/"))
    expect(page.locator(".ai-auth")).to_contain_text("OpenCode CLI(opencode)를 찾지 못했습니다")
    expect(page.get_by_text("키는 MomentForge에 넣지 않습니다", exact=False)).to_be_visible()
    expect(page.get_by_role("button", name="키 저장")).to_have_count(0)
    expect(page.locator("input[type=password]")).to_have_count(0)
    page.locator("section", has=page.get_by_role("heading", name="선택형 AI 검토")).screenshot(path=str(SHOTS / "ai-opencode.png"))

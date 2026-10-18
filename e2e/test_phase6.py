"""Phase 6-A: the cut analysis (docs/AI-CUT-ANALYSIS.md). On the sample,
which has a synthetic transcript with a planted penalty promise, the full
fake provider explains the first candidate, keeps the promise as context in
a setup span and passes both checks; the plain fake leaves the required
context out, so its plan is saved as failed with the reason."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

from test_phase5 import open_destination

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'


def open_sample(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)


def pick_provider(page: Page, provider: str) -> None:
    open_destination(page)
    page.get_by_label("AI 제공자").select_option(label=provider)
    key = page.get_by_label(re.compile(f"^{re.escape(provider)} API 키"))
    if key.count() and "없음" in (key.locator("xpath=..").inner_text()):
        key.fill("test-key")
        page.get_by_role("button", name="키 저장").click()
        expect(page.get_by_label(re.compile(f"^{re.escape(provider)} API 키 \\(메모리에 있음\\)"))).to_be_visible()


def test_cut_analysis_keeps_the_promise_as_setup(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    first = page.locator("ol.candidates > li").first
    first.get_by_label("AI 검토에 포함").check()
    page.get_by_role("button", name="AI 검토로 이동").click()
    page.get_by_role("radio", name="컷 분석").check()
    # Only picked candidates can be cut-analysed: the other scopes are gone.
    expect(page.get_by_label("채팅 구간 직접 지정", exact=False)).to_have_count(0)
    pick_provider(page, "E2E 가짜 AI")
    page.get_by_role("button", name="보낼 내용 미리보기").click()
    preview = page.locator(".ai-preview[data-kind='cut']")
    expect(preview).to_contain_text(re.compile(r"후보 1개, 후보당 4~7회"))
    expect(preview).to_contain_text(re.compile(r"발언 \d+건과 채팅 \d+건"))  # the sample's transcript is read
    preview.get_by_text("보낼 내용 전체").click()
    expect(preview.locator("pre")).to_contain_text('"utt:')
    expect(preview.locator("pre")).to_contain_text("벌칙으로 노래")
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    status = page.locator(".ai-status")
    expect(status).to_contain_text("AI 완료", timeout=15_000)
    expect(status).to_contain_text("컷 계획 1개")
    expect(page.locator(".ai-done")).to_contain_text("AI 컷 분석")
    page.locator(".ai-done").get_by_role("button", name="후보 보기").click()

    note = first.locator(".cut-note")
    expect(note).to_contain_text("검수 통과")
    expect(note.locator(".cut-spans li")).to_have_count(2)
    expect(note.locator(".cut-spans li").first).to_contain_text("앞선 맥락")
    expect(note.locator(".cut-spans li").first).to_contain_text("03:40")  # the promise utterance
    expect(note.locator(".cut-spans li").nth(1)).to_contain_text("본 장면")
    expect(page.locator("ol.candidates > li[data-source='ai']")).to_have_count(0)  # folded under its candidate
    note.screenshot(path=str(SHOTS / "cut-note.png"))

    page.get_by_role("tab", name="파일").click()
    expect(page.get_by_role("button", name=re.compile("컷 구간 EDL"))).to_be_visible()
    with page.expect_download() as info:
        page.get_by_role("button", name=re.compile("컷 구간 CSV")).click()
    csv = Path(info.value.path()).read_text(encoding="utf-8")
    assert csv.startswith("plan_id,order,role,start_ms,end_ms"), csv[:120]
    assert ",setup," in csv and ",main," in csv, csv


def test_cut_analysis_reports_why_a_plan_failed(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    first = page.locator("ol.candidates > li").first
    first.get_by_label("AI 검토에 포함").check()
    page.get_by_role("button", name="AI 검토로 이동").click()
    page.get_by_role("radio", name="컷 분석").check()
    pick_provider(page, "E2E 가짜 AI 2")
    page.get_by_role("button", name="보낼 내용 미리보기").click()
    expect(page.locator(".ai-preview[data-kind='cut']")).to_contain_text("이 모델의 가격 정보가 없어")
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    expect(page.locator(".ai-status")).to_contain_text("AI 완료", timeout=15_000)
    page.get_by_role("tab", name=re.compile("^후보")).click()
    # The newest plan replaces the earlier one under the same candidate.
    note = first.locator(".cut-note")
    expect(note).to_have_count(1)
    expect(note).to_contain_text("검수 실패")
    expect(note).to_contain_text("v2")
    expect(note.locator(".cut-issues")).to_contain_text("필수 맥락 utt:")
    expect(note.locator(".cut-issues")).to_contain_text("고른 span 안에 없습니다")

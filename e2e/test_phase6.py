"""Phases 6-A and 6-D: the cut analysis (docs/AI-CUT-ANALYSIS.md). On the
sample, which has a synthetic transcript with a planted penalty promise, the
full fake provider explains the first candidate, keeps the promise as
context in a setup span and passes both checks; the plain fake leaves the
required context out, so its plan is saved as failed with the reason. Then
the result box: switching boundary candidates, playing evidence, a note,
confirming, the confirmed EDL, and a re-analysis that makes a new version
beside the confirmed one."""
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


def select_row(row) -> None:
    """AI results open in full only under the chosen row: choose it (pressing a chosen row closes it)."""
    head = row.locator(".candidate-head")
    if head.get_attribute("aria-pressed") != "true":
        head.click()
    expect(head).to_have_attribute("aria-pressed", "true")


def pick_provider(page: Page, provider: str) -> None:
    open_destination(page)
    page.get_by_label("AI 제공자").select_option(label=provider)
    key = page.get_by_label(re.compile(f"^{re.escape(provider)} API 키"))
    if key.count() and "없음" in (key.locator("xpath=..").inner_text()):
        key.fill("test-key")
        page.get_by_role("button", name="키 저장").click()
        expect(page.get_by_label(re.compile(f"^{re.escape(provider)} API 키 \\(있음\\)"))).to_be_visible()


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
    expect(preview).to_contain_text(re.compile(r"후보 1개, 후보당 4~11회"))
    expect(preview).to_contain_text("최대 4단계·80KB까지 더 보냅니다")  # the search budget (6-C)
    expect(preview).to_contain_text(re.compile(r"발언 \d+건과 채팅 \d+건"))  # the sample's transcript is read
    preview.get_by_text("보낼 내용 전체").click()
    expect(preview.locator("pre")).to_contain_text('"utt:')
    expect(preview.locator("pre")).to_contain_text("벌칙으로 노래")
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    status = page.locator(".ai-status")
    expect(status).to_contain_text("AI 완료", timeout=15_000)
    expect(status).to_contain_text("컷 분석 1개")
    expect(page.locator(".ai-done")).to_contain_text("AI 컷 분석")
    page.locator(".ai-done").get_by_role("button", name="후보 보기").click()

    # Until the row is chosen the plan is one line; chosen, the whole result box.
    expect(first.locator(".ai-brief", has_text="AI 컷").first).to_contain_text("검수 통과")
    expect(first.locator(".cut-note")).to_have_count(0)
    select_row(first)
    note = first.locator(".cut-note")
    expect(note).to_contain_text("검수 통과")
    expect(note.locator(".cut-search")).to_have_count(0)  # the promise was inside the window: nothing to search
    expect(note.locator(".cut-spans li")).to_have_count(2)
    expect(note.locator(".cut-spans li").first).to_contain_text("앞선 맥락")
    expect(note.locator(".cut-spans li").first).to_contain_text("03:39")  # 0.3 s before the promise utterance (03:40)
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
    select_row(first)
    note = first.locator(".cut-note")
    expect(note).to_have_count(1)
    expect(note).to_contain_text("검수 실패")
    expect(note).to_contain_text("v2")
    expect(note.locator(".cut-issues")).to_contain_text("필수 맥락 utt:")
    expect(note.locator(".cut-issues")).to_contain_text("고른 span 안에 없습니다")


def download_text(page: Page, name: str) -> str:
    with page.expect_download() as info:
        page.get_by_role("button", name=re.compile(name)).click()
    return Path(info.value.path()).read_text(encoding="utf-8")


def add_note(box, text: str) -> None:
    box.get_by_role("button", name="메모 추가").click()
    box.get_by_label("메모 내용").fill(text)
    box.get_by_role("button", name="메모 저장").click()


def test_cut_result_box_notes_confirm_and_new_version(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    first = page.locator("ol.candidates > li").first
    select_row(first)  # the row stays chosen through the steps below
    note = first.locator(".cut-note")
    expect(note).to_contain_text("검수 실패")  # the plain fake's v2 from the test above

    # A note on that version, then "다시 분석": the note goes to the plan step and a new version replaces it.
    add_note(note, "두 번째 시도라 앞선 약속과 연결하면 안 됨")
    expect(note).to_contain_text("메모 1개")
    # A candidate already picked for the review stays picked when one candidate's cut is analysed again.
    page.locator("ol.candidates > li").nth(1).get_by_label("AI 검토에 포함").check()
    note.get_by_role("button", name="다시 분석").click()
    expect(page.get_by_role("radio", name="컷 분석")).to_be_checked()
    target = page.locator(".ai-target")
    expect(target).to_contain_text("후보 1의 컷 분석을 다시 합니다")
    expect(target.get_by_role("button", name="고른 후보 1개 검토로 돌아가기")).to_be_visible()
    pick_provider(page, "E2E 가짜 AI")
    page.get_by_role("button", name="보낼 내용 미리보기").click()
    preview = page.locator(".ai-preview[data-kind='cut']")
    expect(preview).to_contain_text("후보 1개, 후보당")  # the one candidate, not the picks
    expect(preview).to_contain_text("메모 1개를 계획 단계에 함께 보내고")
    preview.get_by_text("보낼 내용 전체").click()
    expect(preview.locator("pre")).to_contain_text('"user_notes"')
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    # Sent: the re-analysis is done asking, and the tab is back to the picks with the kind chosen for them.
    expect(target).to_have_count(0)
    expect(page.get_by_role("radio", name="장면 설명")).to_be_checked()
    expect(page.get_by_role("radio", name="후보 목록에서 고른 1개")).to_be_checked()
    page.get_by_role("tab", name=re.compile("^후보")).click()
    page.locator("ol.candidates > li").nth(1).get_by_label("AI 검토에 포함").uncheck()  # its pick bar would cover the pictures below
    expect(note).to_contain_text("검수 통과", timeout=15_000)
    expect(note).to_have_count(1)  # nothing confirmed yet: the newest version alone
    expect(note).to_contain_text("이 버전에 반영한 메모 1개")
    expect(note).to_contain_text("메모 반영: 두 번째 시도라 앞선 약속과 연결하면 안 됨")  # the note reached the plan step

    # The result box: causes with their evidence, the story's origin, the checks' answers.
    expect(note.locator(".cut-facts")).to_contain_text("첫 발언 뒤 채팅이 몰림")
    expect(note.locator(".cut-alternatives")).to_contain_text("대안 채팅끼리의 농담일 가능성")
    expect(note.locator(".cut-story").first).to_contain_text("03:40")
    note.locator(".cut-story").first.get_by_role("button").click()
    expect(page.locator(".stage-timeline .stage-time")).to_contain_text("03:40")  # the evidence plays from its time
    note.get_by_text("검수 답 보기").click()
    expect(note.locator(".cut-answers")).to_contain_text("지면 벌칙")

    # Boundary candidates: short is the main span alone; the timeline strip follows the switch.
    note.get_by_role("radio", name="짧게").check()
    expect(note.locator(".cut-spans li")).to_have_count(1)
    expect(page.locator(".stage-timeline .timeline-cut")).to_have_count(1)
    note.get_by_role("radio", name="맥락").check()
    expect(note.locator(".cut-spans li")).to_have_count(2)
    expect(page.locator(".stage-timeline .timeline-cut")).to_have_count(2)
    context_total = note.locator(".cut-span-foot").inner_text()
    note.get_by_role("radio", name="전체").check()
    expect(note.locator(".cut-span-foot")).not_to_have_text(context_total)  # the whole event covers more
    expect(note.locator(".cut-span-foot")).to_contain_text("전체 ·")

    # Confirming the whole-event boundary.
    note.get_by_role("button", name="컷 확정").click()
    expect(note).to_contain_text("확정 · 전체")
    note.screenshot(path=str(SHOTS / "cut-result-box.png"))
    page.get_by_role("tab", name="파일").click()
    edl = download_text(page, "컷 확정본 EDL")
    assert edl.startswith("TITLE: MomentForge confirmed cuts"), edl[:80]
    assert edl.count("  AX  ") >= 1 and "* FROM CLIP NAME:" in edl and "00:03:39:21" in edl, edl
    memo = download_text(page, "컷 확정본 editnest")
    assert '"segments"' in memo and '"start": 219.7' in memo, memo[:300]

    # Another note and another analysis: a new version beside the confirmed one, which stays, and so does the EDL.
    page.get_by_role("tab", name=re.compile("^후보")).click()
    add_note(note, "벌칙 인정까지 포함")
    note.get_by_role("button", name="다시 분석").click()
    expect(page.locator(".ai-preview[data-kind='cut']")).to_contain_text("메모 2개를 계획 단계에 함께 보내고")
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    page.get_by_role("tab", name=re.compile("^후보")).click()
    expect(note).to_have_count(2, timeout=15_000)
    kept = first.locator(".cut-note[data-confirmed]")
    newer = first.locator(".cut-note:not([data-confirmed])")
    expect(kept).to_contain_text("확정 · 전체")
    expect(newer).to_contain_text(re.compile(r"확정본 v\d+ 유지"))
    expect(newer).to_contain_text("이 버전에 반영한 메모 2개")
    expect(newer).to_contain_text("메모 반영: 벌칙 인정까지 포함")
    first.locator(".ai-notes").screenshot(path=str(SHOTS / "cut-versions.png"))
    page.get_by_role("tab", name="파일").click()
    assert download_text(page, "컷 확정본 EDL") == edl

"""Phases 6-A and 6-D: the cut analysis (docs/AI-CUT-ANALYSIS.md). On the
sample, which has a synthetic transcript with a planted penalty promise, the
full fake provider explains the first candidate, keeps the promise as
context in a setup span and passes both checks; the plain fake leaves the
required context out, so its plan is saved as failed with the reason. Then
the result box: switching boundary candidates, playing evidence, a note,
confirming, the confirmed EDL, and a re-analysis that makes a new version
beside the confirmed one. Each analysis starts from the candidate's own
"AI 컷 분석" (or a result box's "다시 분석") and goes to the default provider."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

from test_phase4 import use_claude, use_provider

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


def send(page: Page, label: str) -> None:
    """With the automatic review off, a cut analysis is asked once more: the button, then the same in its question."""
    page.get_by_role("group", name=f"{label} 확인").get_by_role("button", name=label).click()


def test_cut_analysis_keeps_the_promise_as_setup(page: Page, base_url: str) -> None:
    use_provider(page, base_url, "E2E 가짜 AI")
    open_sample(page, base_url)
    first = page.locator("ol.candidates > li").first
    select_row(first)
    # A chosen candidate without a cut analysis offers one; it says what goes where before sending.
    first.locator(".cut-start").get_by_role("button", name="AI 컷 분석").click()
    question = page.get_by_role("group", name="AI 컷 분석 확인")
    expect(question).to_contain_text("후보 1의 편집 구간 앞뒤 1분 채팅(가명)과 대본을 E2E 가짜 AI(e2e-model)로 보내")
    expect(question).to_contain_text("요청은 4~11회")
    send(page, "AI 컷 분석")
    note = first.locator(".cut-note")
    expect(note).to_contain_text("검수 통과", timeout=15_000)
    expect(first.locator(".cut-start")).to_have_count(0)  # the result box offers "다시 분석" instead

    # Until the row is chosen the plan is one line; chosen, the whole result box.
    first.locator(".candidate-head").click()
    expect(first.locator(".ai-brief", has_text="AI 컷").first).to_contain_text("검수 통과")
    expect(first.locator(".cut-note")).to_have_count(0)
    select_row(first)
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
    use_provider(page, base_url, "E2E 가짜 AI 2")
    open_sample(page, base_url)
    first = page.locator("ol.candidates > li").first
    select_row(first)
    note = first.locator(".cut-note")
    note.get_by_role("button", name="다시 분석").click()
    expect(page.get_by_role("group", name="다시 분석 확인")).to_contain_text("E2E 가짜 AI 2")
    send(page, "다시 분석")
    # The newest version replaces the earlier one under the same candidate.
    expect(note).to_contain_text("검수 실패", timeout=15_000)
    expect(note).to_have_count(1)
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
    use_provider(page, base_url, "E2E 가짜 AI")
    try:
        check_result_box(page, base_url)
    finally:
        use_claude(page, base_url)


def check_result_box(page: Page, base_url: str) -> None:
    open_sample(page, base_url)
    first = page.locator("ol.candidates > li").first
    select_row(first)  # the row stays chosen through the steps below
    note = first.locator(".cut-note")
    expect(note).to_contain_text("검수 실패")  # the plain fake's v2 from the test above

    # A note on that version, then "다시 분석": the note goes to the plan step and a new version replaces it.
    add_note(note, "두 번째 시도라 앞선 약속과 연결하면 안 됨")
    expect(note).to_contain_text("메모 1개")
    note.get_by_role("button", name="다시 분석").click()
    send(page, "다시 분석")
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
    send(page, "다시 분석")
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

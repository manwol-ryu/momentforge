"""Phase 6-B: a NAS backup folder (chzzk-automation's files) imported as a
session. The fixture is synthetic; the E2E binary's fake speech engine
returns a fixed transcript, so nothing is read from real audio. Phase 6-C:
the fake transcript's promise is at 1:05, before the first candidate's
window, so the cut analysis searches for it, asks before sending what it
found, and links it as the story's origin."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
READY = 'body[data-app-state="ready"]'
FIXTURE = Path(__file__).resolve().parent / "fixtures" / "backup" / "26" / "10"


def test_backup_import_with_transcript_and_donations(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="NAS 백업").click()
    form = page.get_by_role("form", name="NAS 백업 불러오기")
    form.get_by_label("백업 폴더", exact=False).fill(str(FIXTURE))
    form.get_by_role("button", name="폴더 읽기").click()
    row = form.locator(".backup-row", has_text="E2E 가짜 백업 방송")
    expect(row).to_be_visible()
    expect(row).to_contain_text("2026-10-01")
    expect(row).to_contain_text("음성")
    expect(row).to_contain_text("영상 없음")
    expect(form.get_by_label("방송 내용 인식(전사)까지", exact=False)).to_be_checked()
    row.get_by_role("button", name="불러오기").click()
    # The screen follows the job to the new session; chat, analysis, then the transcript.
    expect(page).to_have_url(re.compile(r"/sessions/[0-9a-f]{32}$"), timeout=30_000)
    page.wait_for_selector(READY, timeout=10_000)
    expect(page.get_by_role("heading", level=1, name="E2E 가짜 백업 방송")).to_be_visible()
    expect(page.locator(".job")).to_contain_text(re.compile(r"발언 \d+건을 인식했습니다"), timeout=30_000)
    expect(page.locator("ol.candidates > li").first).to_be_visible(timeout=10_000)

    # Donations and subscriptions show in the chat with their amounts (the playhead moved to 5:05).
    page.get_by_role("slider", name="채팅 볼 위치").fill("305000")
    chat = page.get_by_role("list", name="고른 위치 주변 채팅")
    expect(chat.locator("li[data-kind='donation']")).to_contain_text("💰 5,000원")
    expect(chat.locator("li[data-kind='donation']")).to_contain_text("벌칙 기대합니다")
    expect(chat.locator("li[data-kind='subscription']")).to_contain_text("구독 3개월")
    # Nothing original is kept: author tags are pseudonyms, never the recorder's nicknames.
    assert "시청자" not in chat.inner_text()

    # The transcript tab lists the fake engine's lines and jumps to them.
    page.get_by_role("tab", name="대본").click()
    lines = page.get_by_role("list", name="발언 목록")
    expect(lines.locator("li").first).to_be_visible()
    expect(page.locator("section", has=page.get_by_role("heading", name="대본"))).to_contain_text("이 컴퓨터에서 인식")
    page.get_by_label("대본 찾기").fill("벌칙")
    expect(lines.locator("li")).to_have_count(1)
    expect(lines).to_contain_text("벌칙으로 노래")
    lines.locator("li").first.get_by_role("button").click()
    expect(page.locator(".stage-timeline .stage-time")).to_contain_text("01:05")
    page.screenshot(path=str(SHOTS / "backup-transcript.png"))

    # The cut analysis reads the transcript, but the promise is before the first
    # candidate's window: the preview does not hold it, the search finds it.
    page.get_by_role("tab", name=re.compile("^후보")).click()
    first = page.locator("ol.candidates > li").first
    first.get_by_label("AI 검토에 포함").check()
    page.get_by_role("button", name="AI 검토로 이동").click()
    page.get_by_role("radio", name="컷 분석").check()
    page.get_by_label("검색 결과를 보내기 전에 매번 확인").check()
    from test_phase6 import pick_provider

    pick_provider(page, "E2E 가짜 AI")
    page.get_by_role("button", name="보낼 내용 미리보기").click()
    preview = page.locator(".ai-preview[data-kind='cut']")
    expect(preview).to_contain_text(re.compile(r"발언 \d+건과 채팅 \d+건"))
    expect(preview).to_contain_text("(보내기 전에 매번 확인)")
    preview.get_by_text("보낼 내용 전체").click()
    expect(preview.locator("pre")).to_contain_text(re.compile(r'"kind":\s*"chat"'))
    expect(preview.locator("pre")).not_to_contain_text("벌칙으로 노래")
    page.get_by_role("button", name="확인한 내용으로 AI 실행").click()
    # The run stops with what the search found and waits for the user.
    pending = page.locator(".ai-pending")
    expect(pending).to_contain_text("검색 결과를 보낼까요?", timeout=15_000)
    expect(pending).to_contain_text("발언 1건")
    expect(pending.locator("li")).to_contain_text(["벌칙으로 노래"])
    pending.screenshot(path=str(SHOTS / "cut-search-consent.png"))
    pending.get_by_role("button", name="보내고 계속").click()
    status = page.locator(".ai-status")
    expect(status).to_contain_text("AI 완료", timeout=15_000)
    expect(status).to_contain_text("5/11회 완료")  # analyze twice, plan, two checks
    page.locator(".ai-done").get_by_role("button", name="후보 보기").click()
    note = first.locator(".cut-note")
    expect(note).to_contain_text("검수 통과")
    expect(note.locator(".cut-search")).to_contain_text("검색 1단계로 근거 1건을 더 읽음")
    expect(note.locator(".cut-search")).to_contain_text("이야기 시작 01:05")
    expect(note.locator(".cut-spans li").first).to_contain_text("01:05")  # the found promise is the setup span
    note.screenshot(path=str(SHOTS / "cut-note-searched.png"))

    # The files tab has the caption files beside the chat.
    page.get_by_role("tab", name="파일").click()
    expect(page.get_by_role("button", name=re.compile("대본 SRT"))).to_be_visible()
    expect(page.get_by_role("button", name=re.compile("대본 VTT"))).to_be_visible()

    # Importing again opens the same session instead of a copy.
    page.get_by_role("link", name="새 작업").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="NAS 백업").click()
    form = page.get_by_role("form", name="NAS 백업 불러오기")
    row = form.locator(".backup-row", has_text="E2E 가짜 백업 방송")
    expect(row.get_by_role("link", name="열기")).to_be_visible(timeout=10_000)


CAPTION = Path(__file__).resolve().parent / "fixtures" / "caption" / "e2e.srt"
RUNTIME_AUDIO = Path(__file__).resolve().parent / ".data" / "runtime" / "audio"


def test_caption_file_then_stream_audio_on_a_vod(page: Page, base_url: str) -> None:
    """A VOD without a backup (6-B ⑤⑥): a caption file becomes the transcript
    with its offset applied, and the fake platform's stream is downloaded,
    transcribed by the fake engine and deleted."""
    from test_phase4 import open_video

    open_video(page, base_url, "E2E 가짜 다시보기 61", "https://live.e2e.test/video/61")
    page.get_by_role("tab", name="대본").click()
    panel = page.locator("section", has=page.get_by_role("heading", name="대본"))
    expect(panel).to_contain_text("다시보기의 음성을 받아")
    expect(panel.get_by_role("button", name="방송 내용 인식")).to_be_enabled()

    # A caption file, shifted by two seconds: the 3:33 line lands at 3:35.
    form = panel.get_by_role("form", name="자막 파일 넣기")
    form.get_by_label("자막 파일 (SRT·VTT)").set_input_files(str(CAPTION))
    form.get_by_label("시간 오프셋 (초)").fill("2")
    form.get_by_role("button", name="자막 넣기").click()
    expect(panel).to_contain_text("자막 파일", timeout=10_000)
    expect(panel).to_contain_text("발언 4건")
    lines = page.get_by_role("list", name="발언 목록")
    expect(lines.locator("li").first).to_contain_text("안녕하세요 E2E 자막")
    page.get_by_label("대본 찾기").fill("벌칙")
    expect(lines.locator("li")).to_have_count(1)
    expect(lines).to_contain_text("벌칙으로 노래")  # the <i> tag is gone
    expect(lines).to_contain_text("03:35")
    page.get_by_label("대본 찾기").fill("")
    page.screenshot(path=str(SHOTS / "caption-file.png"))
    page.get_by_role("tab", name="파일").click()
    expect(page.get_by_role("button", name=re.compile("대본 SRT"))).to_be_visible()

    # The stream: downloaded to runtime/audio, recognised, deleted.
    page.get_by_role("tab", name="대본").click()
    panel.get_by_role("button", name="다시 인식").click()
    job = page.locator(".job")
    expect(job).to_contain_text(re.compile("방송 음성 받기|방송 내용 인식"), timeout=10_000)
    expect(job).to_contain_text(re.compile(r"발언 \d+건을 인식했습니다"), timeout=30_000)
    expect(panel).to_contain_text("이 컴퓨터에서 인식", timeout=10_000)
    expect(panel).to_contain_text("발언 40건")
    assert not RUNTIME_AUDIO.exists() or not any(RUNTIME_AUDIO.iterdir()), list(RUNTIME_AUDIO.iterdir())
    page.screenshot(path=str(SHOTS / "stream-transcript.png"))

    # A file that is not a caption is refused with a reason, and the transcript stays.
    page.get_by_text("자막 파일로 바꾸기").click()
    form = panel.get_by_role("form", name="자막 파일 넣기")
    form.get_by_label("자막 파일 (SRT·VTT)").set_input_files(
        {"name": "note.srt", "mimeType": "text/plain", "buffer": "그냥 메모입니다\n".encode()}
    )
    form.get_by_role("button", name="자막 넣기").click()
    expect(form.get_by_role("alert")).to_contain_text("SRT 또는 WebVTT 자막 파일이 아닙니다")
    expect(panel).to_contain_text("발언 40건")


# The rejected non-caption file answers 400 on purpose.
test_caption_file_then_stream_audio_on_a_vod.allow_console = ["status of 400"]

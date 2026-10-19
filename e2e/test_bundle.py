"""Session backup bundle (docs/AI-CUT-ANALYSIS.md 2.5): the Files tab writes
one folder with the chat, analysis, timeline, notes and the video. The E2E
binary's fake platform "downloads" 40 placeholder segments, slowly enough
to stop the job midway and resume it."""
from __future__ import annotations

import json
import re
from pathlib import Path

from playwright.sync_api import Page, expect

SHOTS = Path(__file__).resolve().parent / ".data" / "screenshots"
BACKUPS = Path(__file__).resolve().parent / ".data" / "backups"
READY = 'body[data-app-state="ready"]'


def bundle_panel(page: Page):
    page.get_by_role("tab", name="파일").click()
    return page.locator("section", has=page.get_by_role("heading", name="백업 묶음"))


def session_id(page: Page) -> str:
    return page.url.rstrip("/").split("/")[-1]


def test_bundle_with_video_then_refresh(page: Page, base_url: str) -> None:
    from test_phase4 import open_video

    open_video(page, base_url, "E2E 가짜 다시보기 62", "https://live.e2e.test/video/62")
    sid = session_id(page)
    panel = bundle_panel(page)
    expect(panel).to_contain_text("만들 위치")
    expect(panel).to_contain_text(sid)
    video = panel.get_by_label("영상 포함 (720p 이하)", exact=False)
    expect(video).to_be_checked()  # the settings' default
    panel.get_by_role("button", name="백업 묶음 만들기").click()

    job = page.locator(".job")
    expect(job).to_contain_text("백업 묶음 영상 받기", timeout=10_000)
    expect(job).to_contain_text(re.compile(r"\d+/40 조각"))
    expect(job).to_contain_text("백업 묶음을 만들었습니다", timeout=30_000)
    expect(job).to_contain_text("영상 포함")
    expect(panel).to_contain_text("영상 포함 (video.mp4)")
    expect(panel).to_contain_text(re.compile(r"파일 \d+개"))
    expect(panel.get_by_role("button", name="백업 묶음 갱신")).to_be_enabled()
    page.screenshot(path=str(SHOTS / "bundle-done.png"))

    folder = BACKUPS / sid
    manifest = json.loads((folder / "manifest.json").read_text())
    assert manifest["format"] == "momentforge-backup" and manifest["version"] == 1
    assert manifest["video"]["state"] == "complete"
    assert manifest["time_axis"]["video_offset_ms"] == 0
    paths = {f["path"] for f in manifest["files"]}
    for want in ("chat.jsonl", "chat.txt", "timeline/timeline.json", "notes.md", "video.mp4", "video.json"):
        assert want in paths, want
    assert (folder / "video.mp4").stat().st_size == 19 + 40 * 4096
    assert not (folder / "video.mp4.part").exists()
    assert "백업 묶음" in (folder / "README.txt").read_text()

    # Refreshing rewrites nothing that did not change; the video stays.
    panel.get_by_role("button", name="백업 묶음 갱신").click()
    expect(job).to_contain_text("바뀐 파일 0개", timeout=10_000)
    expect(panel).to_contain_text("영상 포함 (video.mp4)")


def test_bundle_video_stops_and_resumes(page: Page, base_url: str) -> None:
    from test_phase4 import open_video

    open_video(page, base_url, "E2E 가짜 다시보기 63", "https://live.e2e.test/video/63")
    sid = session_id(page)
    panel = bundle_panel(page)
    panel.get_by_role("button", name="백업 묶음 만들기").click()
    job = page.locator(".job")
    expect(job).to_contain_text(re.compile(r"([5-9]|\d\d)/40 조각"), timeout=10_000)
    job.get_by_role("button", name="작업 중단").click()
    expect(job).to_contain_text("작업을 중단했습니다", timeout=10_000)
    expect(panel).to_contain_text("영상 받다가 멈춤")
    manifest = json.loads((BACKUPS / sid / "manifest.json").read_text())
    assert manifest["video"]["state"] == "partial"  # the text files are already written
    assert "chat.jsonl" in {f["path"] for f in manifest["files"]}

    panel.get_by_role("button", name="영상 이어받기").click()
    expect(job).to_contain_text("멈춘 곳부터 영상을 이어받는", timeout=10_000)
    expect(job).to_contain_text("백업 묶음을 만들었습니다", timeout=30_000)
    expect(panel).to_contain_text("영상 포함 (video.mp4)")
    assert (BACKUPS / sid / "video.mp4").stat().st_size == 19 + 40 * 4096


def test_bundle_settings(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("link", name="설정").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="백업 묶음").click()
    folder = page.get_by_label("폴더 경로")
    expect(folder).to_have_attribute("placeholder", re.compile(r"backups$"))
    folder.fill("상대/경로")
    page.get_by_role("button", name="기본값 저장").click()
    expect(page.get_by_role("alert")).to_contain_text("전체 경로")
    folder.fill("")
    page.get_by_role("radio", name="480p").check()
    page.get_by_role("button", name="기본값 저장").click()
    expect(page.get_by_text("기본값을 저장했습니다.")).to_be_visible()
    page.screenshot(path=str(SHOTS / "settings-bundle.png"))
    page.reload()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="백업 묶음").click()
    expect(page.get_by_role("radio", name="480p")).to_be_checked()
    # Put the default back for the other tests in this run.
    page.get_by_role("radio", name="720p").check()
    page.get_by_role("button", name="기본값 저장").click()
    expect(page.get_by_text("기본값을 저장했습니다.")).to_be_visible()


test_bundle_settings.allow_console = ["status of 400"]  # the relative folder is refused on purpose

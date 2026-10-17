"""The settings screen (docs/DESIGN-SYSTEM.md 4): opened from the sidebar,
it keeps a fake provider's key on this computer and clears it, and the
defaults it saves pre-fill the new-task forms and the AI review tab. The
key is the E2E fake provider's, so nothing leaves the machine."""
from __future__ import annotations

import re
from pathlib import Path

from playwright.sync_api import Page, expect

DATA = Path(__file__).resolve().parent / ".data"
SHOTS = DATA / "screenshots"
READY = 'body[data-app-state="ready"]'
FAKE = "E2E 가짜 AI"
FAKE_KEY = "fake-key-e2e-123"


def open_settings(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("link", name="설정").click()
    expect(page).to_have_url(re.compile(r"/settings$"))
    page.wait_for_selector(READY, timeout=10_000)


def fake_provider(page: Page):
    return page.get_by_role("region", name=FAKE, exact=True)


def full_shot(page: Page, name: str) -> None:
    """The whole page in one window-sized capture. A full-page capture would draw
    the sticky sidebar and save row where they sit in the smaller window."""
    size = page.viewport_size
    page.set_viewport_size({"width": size["width"], "height": page.evaluate("document.documentElement.scrollHeight")})
    page.screenshot(path=str(SHOTS / name))
    page.set_viewport_size(size)


def save_defaults(page: Page) -> None:
    page.get_by_role("button", name="기본값 저장").click()
    expect(page.get_by_role("status").filter(has_text="기본값을 저장했습니다")).to_be_visible()


def test_settings_keep_a_key_and_prefill_new_tasks(page: Page, base_url: str) -> None:
    open_settings(page, base_url)
    expect(page.get_by_role("heading", level=1, name="설정")).to_be_visible()
    expect(page.get_by_role("link", name="설정")).to_have_attribute("aria-current", "page")
    expect(page).to_have_title("설정 - MomentForge")
    expect(page.get_by_role("button", name="기본값 저장")).to_be_disabled()  # nothing changed yet
    full_shot(page, "settings-light.png")

    # Only the chosen provider's card is shown; choosing another shows its card instead.
    expect(page.locator("section.provider")).to_have_count(1)
    expect(page.get_by_role("region", name="Claude (Anthropic)", exact=True)).to_be_visible()
    page.get_by_role("radio", name=FAKE, exact=True).check()
    expect(page.locator("section.provider")).to_have_count(1)
    expect(page.get_by_role("region", name="Claude (Anthropic)", exact=True)).to_have_count(0)

    # A key kept on this computer: in memory now, and in the data folder's .env for the next run.
    fake = fake_provider(page)
    clear = fake.get_by_role("button", name="키 지우기")
    if clear.count():  # earlier tests in this run keep a key in memory
        clear.click()
    expect(fake.locator(".provider-state")).to_have_text("키 없음")
    fake.get_by_label(re.compile(f"^{FAKE} API 키")).fill(FAKE_KEY)
    fake.get_by_label("이 컴퓨터에도 저장", exact=False).check()
    expect(fake.get_by_text("평문으로 저장합니다", exact=False)).to_be_visible()
    fake.get_by_role("button", name="키 저장").click()
    expect(fake.get_by_label(re.compile(f"^{FAKE} API 키 \\(메모리에 있음\\)"))).to_be_visible()
    expect(fake.locator(".provider-state")).to_have_text("키 있음")
    expect(fake.get_by_text("이 컴퓨터(데이터 폴더의 .env)에 저장한 키를 씁니다", exact=False)).to_be_visible()
    expect(fake.get_by_label(re.compile(f"^{FAKE} API 키"))).to_have_value("")  # never shown again
    env = (DATA / ".env").read_text()
    assert f"MOMENTFORGE_E2EFAKE_API_KEY={FAKE_KEY}\n" in env, env
    fake.screenshot(path=str(SHOTS / "settings-provider.png"))

    # Defaults: this provider and model for the AI review, and the forms' options.
    fake.get_by_label("기본 모델 ID").fill("e2e-model-b")
    page.get_by_role("tab", name="새 작업 기본값").click()
    page.get_by_label("항상 가장 민감하게", exact=False).check()
    page.get_by_label("영상 댓글의 시청자 추천도 수집", exact=False).uncheck()
    page.get_by_label("시청자 수 변화 보정", exact=False).uncheck()
    expect(page.get_by_text("저장하지 않은 변경이 있습니다", exact=False)).to_be_visible()
    save_defaults(page)
    saved = (DATA / "settings.env").read_text()
    for line in ["AI_PROVIDER=e2efake", "AI_MODEL_E2EFAKE=e2e-model-b", "COLLECT_SENSITIVE=true", "COLLECT_COMMENTS=false",
                 "LIVE_VIEWER_ADAPTIVE=false"]:
        assert line + "\n" in saved, saved
    assert FAKE_KEY not in saved

    # The new-task forms start from them.
    page.get_by_role("link", name="새 작업").click()
    page.wait_for_selector(READY, timeout=10_000)
    vod = page.get_by_role("tabpanel", name="다시보기")
    expect(vod.get_by_label("항상 가장 민감하게", exact=False)).to_be_checked()
    expect(vod.get_by_label("영상 댓글의 시청자 추천도 수집", exact=False)).not_to_be_checked()
    page.get_by_role("tab", name="생방송").click()
    live = page.get_by_role("tabpanel", name="생방송")
    expect(live.get_by_label("항상 가장 민감하게", exact=False)).to_be_checked()
    expect(live.get_by_label("시청자 수 변화 보정", exact=False)).not_to_be_checked()

    # So does the AI review, with the same key state; clearing it there clears it here too.
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("tab", name="AI 검토").click()
    expect(page.get_by_label("AI 제공자").locator("option:checked")).to_have_text(FAKE)
    expect(page.get_by_label("모델 ID", exact=True)).to_have_value("e2e-model-b")
    expect(page.get_by_label(re.compile(f"^{FAKE} API 키 \\(메모리에 있음\\)"))).to_be_visible()
    expect(page.get_by_label("이 컴퓨터에도 저장", exact=False)).to_be_checked()
    page.get_by_role("button", name="키 지우기").click()
    expect(page.get_by_label(re.compile(f"^{FAKE} API 키 \\(없음\\)"))).to_be_visible()
    assert not (DATA / ".env").exists(), "the last kept key gone, the key file goes too"

    open_settings(page, base_url)
    fake = fake_provider(page)
    expect(fake.locator(".provider-state")).to_have_text("키 없음")
    expect(page.get_by_role("radio", name=FAKE, exact=True)).to_be_checked()  # saved defaults survive a reload
    expect(fake.get_by_label("기본 모델 ID")).to_have_value("e2e-model-b")

    # Back to the built-in defaults so later tests start as before.
    fake.get_by_label("기본 모델 ID").fill("")
    page.get_by_role("radio", name="Claude (Anthropic)").check()
    page.get_by_role("tab", name="새 작업 기본값").click()
    page.get_by_label("항상 가장 민감하게", exact=False).uncheck()
    page.get_by_label("영상 댓글의 시청자 추천도 수집", exact=False).check()
    page.get_by_label("시청자 수 변화 보정", exact=False).check()
    save_defaults(page)


def test_settings_from_the_folded_rail(page: Page, base_url: str) -> None:
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("button", name="작업 목록 접기").click()
    page.get_by_role("link", name="설정").click()  # the rail's
    expect(page.get_by_role("heading", level=1, name="설정")).to_be_visible()
    expect(page.get_by_role("link", name="설정")).to_have_attribute("aria-current", "page")
    page.get_by_role("button", name="작업 목록 펼치기").click()


def test_settings_on_a_phone_and_in_dark_mode(page: Page, base_url: str) -> None:
    page.set_viewport_size({"width": 375, "height": 812})
    page.goto(base_url)
    page.wait_for_selector(READY, timeout=10_000)
    page.get_by_role("button", name="작업 목록").click()  # the drawer holds the way to the settings
    page.get_by_role("link", name="설정").click()
    page.wait_for_selector(READY, timeout=10_000)
    expect(page.get_by_role("heading", level=1, name="설정")).to_be_visible()
    overflow = page.evaluate("document.documentElement.scrollWidth - document.documentElement.clientWidth")
    assert overflow <= 0, f"horizontal overflow {overflow}px"
    full_shot(page, "settings-mobile.png")
    page.get_by_role("tab", name="새 작업 기본값").click()
    full_shot(page, "settings-mobile-collect.png")

    page.set_viewport_size({"width": 1280, "height": 900})
    page.emulate_media(color_scheme="dark")
    page.get_by_role("tab", name="AI 검토").click()
    bg = page.evaluate("getComputedStyle(document.body).backgroundColor")
    assert bg == "rgb(28, 28, 28)", bg
    full_shot(page, "settings-dark.png")

"""The settings screen (docs/DESIGN-SYSTEM.md 4): opened from the sidebar on
its 일반 설정 tab, it keeps a fake provider's key on this computer and clears
it, the defaults it saves pre-fill the new-task forms and choose where AI
work goes, and the automatic AI review turns on only after agreeing to what
it sends. The key is the E2E fake provider's, so nothing leaves the machine."""
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
    expect(page.get_by_role("tab", name="일반 설정")).to_have_attribute("aria-selected", "true")  # the first tab
    full_shot(page, "settings-light.png")
    page.get_by_role("tab", name="AI 검토").click()

    # Only the chosen provider's card is shown; choosing another shows its card instead.
    expect(page.locator("section.provider")).to_have_count(1)
    expect(page.get_by_role("region", name="Claude (Anthropic)", exact=True)).to_be_visible()
    page.get_by_role("radio", name=FAKE, exact=True).check()
    expect(page.locator("section.provider")).to_have_count(1)
    expect(page.get_by_role("region", name="Claude (Anthropic)", exact=True)).to_have_count(0)
    # Looking at a provider does not make it the default: nothing to save yet, and its card offers it.
    expect(fake_provider(page).get_by_role("button", name="기본 제공자로 쓰기")).to_be_visible()
    expect(page.get_by_role("button", name="기본값 저장")).to_be_disabled()

    # A key kept on this computer: in memory now, and in the data folder's .env for the next run.
    fake = fake_provider(page)
    clear = fake.get_by_role("button", name="키 지우기")
    if clear.count():  # earlier tests in this run keep a key in memory
        clear.click()  # asked once more: the key goes from memory (and .env)
        fake.get_by_role("group", name="키 지우기 확인").get_by_role("button", name="키 지우기").click()
    expect(fake.locator(".provider-state")).to_have_text("키 없음")
    fake.get_by_label(re.compile(f"^{FAKE} API 키")).fill(FAKE_KEY)
    fake.get_by_label("이 컴퓨터에도 저장", exact=False).check()
    expect(fake.get_by_text("평문으로 저장합니다", exact=False)).to_be_visible()
    fake.get_by_role("button", name="키 저장").click()
    expect(fake.get_by_label(re.compile(f"^{FAKE} API 키 \\(있음\\)"))).to_be_visible()
    expect(fake.locator(".provider-state")).to_have_text("키 있음")
    expect(fake.get_by_text("이 컴퓨터(데이터 폴더의 .env)에 저장한 키를 씁니다", exact=False)).to_be_visible()
    expect(fake.get_by_label(re.compile(f"^{FAKE} API 키"))).to_have_value("")  # never shown again
    env = (DATA / ".env").read_text()
    assert f"MOMENTFORGE_E2EFAKE_API_KEY={FAKE_KEY}\n" in env, env
    fake.screenshot(path=str(SHOTS / "settings-provider.png"))

    # Defaults: this provider and model for the AI review, and the forms' options.
    fake.get_by_role("button", name="기본 제공자로 쓰기").click()
    expect(fake.locator(".provider-default")).to_contain_text("기본 제공자입니다")
    fake.get_by_label("기본 모델 ID").fill("e2e-model-b")
    page.get_by_role("tab", name="일반 설정").click()
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

    # AI work goes to it: the sample's AI line offers it (the sample is never sent by itself).
    page.get_by_role("button", name="샘플로 먼저 체험").click()
    page.wait_for_selector(READY, timeout=10_000)
    bar = page.locator(".ai-bar")
    expect(bar).to_contain_text("샘플은 저절로 AI로 보내지 않습니다.")
    bar.get_by_role("button", name="AI 장면 설명 받기").click()
    expect(page.get_by_role("group", name="AI 장면 설명 받기 확인")).to_contain_text(f"{FAKE}로 보내")
    page.get_by_role("group", name="AI 장면 설명 받기 확인").get_by_role("button", name="보내지 않기").click()

    # Clearing the key is asked once more and takes it from .env too.
    open_settings(page, base_url)
    page.get_by_role("tab", name="AI 검토").click()
    fake = fake_provider(page)
    expect(fake.get_by_label("이 컴퓨터에도 저장", exact=False)).to_be_checked()
    fake.get_by_role("button", name="키 지우기").click()
    clearing = fake.get_by_role("group", name="키 지우기 확인")
    expect(clearing).to_contain_text("이 컴퓨터(.env)에서 키를 지웁니다")
    clearing.get_by_role("button", name="키 지우기").click()
    expect(fake.get_by_label(re.compile(f"^{FAKE} API 키 \\(없음\\)"))).to_be_visible()
    assert not (DATA / ".env").exists(), "the last kept key gone, the key file goes too"

    open_settings(page, base_url)
    page.get_by_role("tab", name="AI 검토").click()
    fake = fake_provider(page)
    expect(fake.locator(".provider-state")).to_have_text("키 없음")
    expect(page.get_by_role("radio", name=FAKE, exact=True)).to_be_checked()  # saved defaults survive a reload
    expect(fake.get_by_label("기본 모델 ID")).to_have_value("e2e-model-b")

    # Back to the built-in defaults so later tests start as before.
    fake.get_by_label("기본 모델 ID").fill("")
    page.get_by_role("radio", name="Claude (Anthropic)").check()
    page.get_by_role("region", name="Claude (Anthropic)", exact=True).get_by_role("button", name="기본 제공자로 쓰기").click()
    page.get_by_role("tab", name="일반 설정").click()
    page.get_by_label("항상 가장 민감하게", exact=False).uncheck()
    page.get_by_label("영상 댓글의 시청자 추천도 수집", exact=False).check()
    page.get_by_label("시청자 수 변화 보정", exact=False).check()
    save_defaults(page)


def test_settings_turn_on_auto_ai_only_after_agreeing(page: Page, base_url: str) -> None:
    """일반 설정: what runs by itself once candidates are found. Speech
    recognition is on by default; the AI review is off, and ticking it first
    shows what is sent and the limits, to agree to or not."""
    open_settings(page, base_url)
    auto = page.get_by_role("group", name="편집 후보가 나오면 자동으로")
    expect(auto.get_by_role("checkbox", name=re.compile("^다시보기 방송 내용 인식"))).to_be_checked()
    expect(auto.get_by_role("checkbox", name=re.compile("^생방송 음성도 받아 후보 구간 인식"))).to_be_checked()
    ai = auto.get_by_role("checkbox", name=re.compile("^AI 검토 자동 실행"))
    expect(ai).not_to_be_checked()

    ai.click()
    consent = page.get_by_role("group", name="AI 검토 자동 실행 확인")
    expect(consent).to_contain_text("켜면 편집 후보가 나올 때마다 묻지 않고 보냅니다.")
    expect(consent).to_contain_text("보내지 않는 것: 영상·음성")
    expect(consent).to_contain_text("한도: 작업마다 요청 60회")
    full_shot(page, "settings-auto-consent.png")
    consent.get_by_role("button", name="켜지 않기").click()
    expect(consent).to_have_count(0)
    expect(ai).not_to_be_checked()
    expect(page.get_by_role("button", name="기본값 저장")).to_be_disabled()

    ai.click()
    page.get_by_role("group", name="AI 검토 자동 실행 확인").get_by_role("button", name="동의하고 켜기").click()
    expect(ai).to_be_checked()
    expect(auto.get_by_role("checkbox", name=re.compile("^AI로 대본 다듬기"))).to_be_checked()
    auto.get_by_label("작업마다 최대 요청").fill("40")
    save_defaults(page)
    saved = (DATA / "settings.env").read_text()
    for line in ["AUTO_AI_REVIEW=true", "AUTO_AI_MAX_REQUESTS=40", "AUTO_AI_TRANSCRIPT_FIX=true", "AUTO_TRANSCRIBE=true",
                 "LIVE_RECORD_AUDIO=true"]:
        assert line + "\n" in saved, saved
    # Saved, it stays on after a reload.
    page.reload()
    page.wait_for_selector(READY, timeout=10_000)
    expect(ai).to_be_checked()

    # Back to off and the default limit for later tests.
    auto.get_by_label("작업마다 최대 요청").fill("60")
    ai.click()
    expect(ai).not_to_be_checked()
    save_defaults(page)
    assert "AUTO_AI_REVIEW=false\n" in (DATA / "settings.env").read_text()


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
    full_shot(page, "settings-mobile.png")  # 일반 설정, the first tab
    page.get_by_role("tab", name="AI 검토").click()
    full_shot(page, "settings-mobile-ai.png")

    page.set_viewport_size({"width": 1280, "height": 900})
    page.emulate_media(color_scheme="dark")
    page.get_by_role("tab", name="AI 검토").click()
    bg = page.evaluate("getComputedStyle(document.body).backgroundColor")
    assert bg == "rgb(28, 28, 28)", bg
    full_shot(page, "settings-dark.png")


def test_settings_show_speech_engines_and_keep_whisper_paths(page: Page, base_url: str) -> None:
    """The 방송 내용 인식 tab lists every engine with its state; whisper.cpp's
    paths are kept in settings.env and its state says what is still missing."""
    open_settings(page, base_url)
    page.get_by_role("tab", name="방송 내용 인식").click()
    engines = page.get_by_role("list", name="음성 인식 엔진")
    fake = engines.locator("section[data-engine='e2efake']")
    whisper = engines.locator("section[data-engine='whispercpp']")
    expect(fake.locator(".provider-state")).to_have_text("준비됨")
    expect(fake).to_contain_text("지금 쓰는 엔진")
    expect(whisper.locator(".provider-state")).to_have_text("준비 안 됨")
    expect(whisper).to_contain_text("whisper-cli 실행파일 경로를 적어 주세요")
    expect(page.get_by_role("radio", name="자동")).to_be_checked()
    expect(page.get_by_text(re.compile("자동: 지금은 준비된 첫 엔진인 E2E 가짜 음성 인식"))).to_be_visible()
    full_shot(page, "settings-asr.png")

    whisper.get_by_label("whisper-cli 실행파일").fill("/nonexistent/momentforge-e2e/whisper-cli")
    whisper.get_by_label("모델 파일(.bin)").fill("/nonexistent/momentforge-e2e/ggml-large-v3-turbo-q5_0.bin")
    page.get_by_role("radio", name="whisper.cpp (whisper-cli)").check()
    save_defaults(page)
    saved = (DATA / "settings.env").read_text()
    for line in ["ASR_ENGINE=whispercpp", "ASR_WHISPERCPP_BIN=/nonexistent/momentforge-e2e/whisper-cli",
                 "ASR_WHISPERCPP_MODEL=/nonexistent/momentforge-e2e/ggml-large-v3-turbo-q5_0.bin"]:
        assert line + "\n" in saved, saved
    # Saving re-checks the engines: the program is still missing, so the fake stays the one in use.
    expect(whisper).to_contain_text("whisper-cli를 찾지 못했습니다: /nonexistent/momentforge-e2e/whisper-cli")
    expect(fake).to_contain_text("지금 쓰는 엔진")

    # Back to the defaults for later tests.
    open_settings(page, base_url)
    page.get_by_role("tab", name="방송 내용 인식").click()
    whisper = page.get_by_role("list", name="음성 인식 엔진").locator("section[data-engine='whispercpp']")
    expect(whisper.get_by_label("whisper-cli 실행파일")).to_have_value("/nonexistent/momentforge-e2e/whisper-cli")
    whisper.get_by_label("whisper-cli 실행파일").fill("")
    whisper.get_by_label("모델 파일(.bin)").fill("")
    page.get_by_role("radio", name="자동").check()
    save_defaults(page)
    assert "ASR_" not in (DATA / "settings.env").read_text()

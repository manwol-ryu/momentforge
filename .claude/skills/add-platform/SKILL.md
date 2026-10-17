---
name: add-platform
description: Add or update a streaming platform adapter in MomentForge (플랫폼 추가·어댑터 수정) — implement platform.Platform plus the optional VODSource, PlaybackSource and LinkSource in internal/platform/{id}, use the shared polite HTTP client, validate every response shape, register it in cmd/momentforge, and test with a fake server. Use when the user wants SOOP(아프리카TV), YouTube, Twitch or another platform supported, when CHZZK changes its API and collection fails with SCHEMA_CHANGED or BOUNDARY_CHANGED, or when changing how URLs, chat pages, playback or watch links are handled.
license: MIT
compatibility: Designed for Claude Code. Needs Go 1.26+. Checking against the real platform needs network access and the user's go-ahead.
metadata:
  project: momentforge
  design-doc: docs/REDESIGN.md
---

# 플랫폼 어댑터 추가·수정

어댑터는 "이 플랫폼의 주소와 응답을 MomentForge의 공통 형식으로 바꾸는 일"만 합니다. 저장, 가명화, 재시도 정책, 이어받기, 분석은 `internal/app`이 맡습니다. 기준 구현은 `internal/platform/chzzk/`입니다.

## 1. 무엇을 구현하나

`internal/platform/platform.go`의 인터페이스 중 플랫폼이 지원하는 것만 구현합니다. 지원하지 않는 기능의 인터페이스는 구현하지 않습니다. 화면은 이것을 보고 버튼을 보이거나 숨깁니다(`canPlay`, `canResume`).

| 인터페이스 | 필수 | 하는 일 |
|---|---|---|
| `Platform` | 예 | `ID()`(영문 소문자), `Name()`(화면 이름), `Match(raw)`, `MediaOrigins()` |
| `VODSource` | 다시보기 수집 시 | `VODMeta`(제목·길이), `VODChats`(커서부터 한 페이지) |
| `PlaybackSource` | 앱 안 재생 시 | 공개 HLS 주소. 서명된 주소이므로 저장하지 않음 |
| `LinkSource` | 권장 | 플랫폼 자체 플레이어의 해당 시각 링크 |
| `LiveSource` | 3단계 | 생방송 채팅 스트림 |
| `LivePlaybackSource` | 생방송 앱 안 재생 시 | 채널의 지금 방송 공개 HLS 주소. 방송 중이 아니면 `LIVE_OFFLINE`. 저장하지 않으며, 영상 CDN이 여러 곳이면 모두 `MediaOrigins()`에 넣음 |

## 2. 구현 규칙

- **위치**: `internal/platform/<id>/`. 플랫폼 도메인과 엔드포인트 문자열은 이 폴더 안에만 둡니다. 구조 검사가 다른 곳에 있으면 막습니다(새 플랫폼이면 `check_layers.py`의 `LITERAL_RULES`에 그 도메인 규칙을 추가합니다).
- **`Match`**: 다른 플랫폼 주소면 `ok=false, err=nil`을 돌려줍니다. 자기 도메인인데 형식이 틀리면 `INVALID_URL` UserError로 올바른 형식을 알려줍니다. https, 사용자 정보 없음, 표준 포트만 허용합니다.
- **HTTP**: `platform.HTTPClient`를 씁니다. 요청 간격, 재시도, `Retry-After`, 크기 제한, 리다이렉트 호스트 제한이 들어 있습니다. 플랫폼이 허용하는 수준보다 빠르게 요청하지 않습니다.
- **응답 검증**: 예상과 다른 모양은 추측해서 넘기지 말고 `SCHEMA_CHANGED`(`platform.ErrSchemaChanged`를 감쌈)로 실패시킵니다. 페이지를 거부할 때는 `*platform.PageError{Raw, Err}`로 원문을 함께 돌려줍니다. app이 격리 보관합니다.
- **커서**: `VODChats(cursor)`는 같은 커서에 같은 페이지를 줘야 합니다. 이어받기는 저장된 커서부터 다시 요청합니다. 끝이면 `Finished=true`입니다. 경계를 보장할 수 없는 구간은 `Gaps`로 알립니다.
- **작성자**: `RawChat.AuthorID`에 원본 ID를 담기만 합니다. 가명화(`privacy.AuthorKey`)는 app이 합니다. 닉네임과 프로필은 담지 않습니다.
- **오류 문구**: 사용자가 할 일을 알려주는 한국어로 씁니다. 접근 불가는 `ACCESS_DENIED`, 로그인 필요는 `LOGIN_REQUIRED`(로그인 수집은 보류 중)처럼 기존 코드를 재사용합니다.
- **테스트용 옵션**: `WithAPIBase`, `WithInterval`, `WithSleep`처럼 옵션 함수로 주소와 대기를 바꿀 수 있게 합니다. 실행파일에 환경변수로 주소를 바꾸는 기능은 만들지 않습니다(데이터가 다른 곳으로 새는 통로가 됨).

## 3. 등록

`cmd/momentforge/server.go`의 `registerPlatforms`에 `r.Register(<id>.New())`를 추가합니다. `MediaOrigins()`는 CSP에 자동으로 들어갑니다. 홈 화면의 입력 안내 문구가 한 플랫폼만 가리키고 있다면 함께 고칩니다.

## 4. 테스트

1. **어댑터 단위 테스트** (`chzzk_test.go`를 본보기로):
   - `httptest` 가짜 서버로 정상 응답, 각 오류 상태, 형식 변경, 페이지 경계 위반, 다음 커서 불일치, 재시도 후 포기, 다른 호스트 리다이렉트 거부, 취소, 재생 주소 검증을 확인합니다.
   - 테스트 데이터는 지어낸 값만 씁니다. 실제 응답을 참고했다면 닉네임, ID, 본문을 가짜로 바꿉니다.
2. **통합 테스트**: `cmd/momentforge/server_test.go`의 `TestCollectThroughAPIWithFakeChzzk`처럼 `options.Platforms`에 가짜 서버를 가리키는 어댑터를 넣고, HTTP API로 수집부터 재생 주소까지 확인합니다.
3. e2e-check 스킬로 `make test`와 `make e2e`를 실행합니다.
4. **실제 확인**: 실제 플랫폼 주소로 수집과 재생을 확인하는 것은 외부 서비스 호출입니다. 사용자에게 주소를 받고 알린 뒤 진행합니다.

## 5. 플랫폼 응답이 바뀌었을 때 (CHZZK 등)

1. 작업의 오류 코드와 문구를 확인합니다. `SCHEMA_CHANGED`, `BOUNDARY_CHANGED`, `PAGE_STALLED`가 대표적입니다.
2. 데이터 폴더 DB의 `raw_pages`에서 `quarantined=1`인 원문을 보고 무엇이 바뀌었는지 찾습니다. 원문에는 개인정보가 있을 수 있으니 대화나 파일에 그대로 옮기지 않습니다.
3. 파싱을 고치고, 바뀐 모양을 재현하는 가짜 응답으로 테스트를 추가합니다.
4. 사용자는 저장된 작업에서 **이어받기**로 멈춘 위치부터 계속할 수 있습니다.

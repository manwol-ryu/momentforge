# MomentForge 재설계 문서 (Go + React)

작성일: 2026-10-16 · 개정: 2026-10-16 (anthropics/skills 반영, 확인 사항 결정) · 상태: **확정, 0단계 준비** · 기준 코드: `legacy-python` 브랜치 (CzzEdit 0.3.2)

## 1. 인터뷰 결정 사항

| 항목 | 결정 |
|---|---|
| 목표 | 효율성과 이후 확장성. 포크한 코드이므로 구조를 새로 이해·소유할 수 있는 형태로 재작성 |
| 백엔드 | **Go** 단일 실행파일 |
| 배포 | 로컬 서버(127.0.0.1) + 브라우저. UI 빌드 결과를 실행파일에 내장(`embed`) |
| 프론트엔드 | **React + TypeScript** (Vite) |
| 호환성 | 기존 기능 동등 우선. 기존 `.czzedit` DB는 **호환하지 않음** (새 스키마로 시작) |
| 브랜치 | 기존 Python 코드는 `legacy-python`, 새 코드는 `main` |
| 테스트 | 사전 골든 테스트 없음. **구현 후 정상 작동 테스트** |
| 확장 방향 | 다른 플랫폼, 탐지 규칙 추가·조정, LIVE 고도화, AI 분석 강화 |
| 확장 1순위 | **AI 분석 강화** (여러 제공자, 자동 분석) |
| 앱 이름 | **MomentForge**로 변경. 실행파일·Go 모듈·데이터 폴더는 `momentforge` |
| Go 버전 | **1.26 이상** (처음 1.23으로 정했으나 `modernc.org/sqlite` 최신판 요구로 상향, 15절) |
| AI 기본 제공자 | 5단계부터 **Claude가 기본**, OpenAI는 선택 |
| MCP 서버 | **5단계에 포함** |
| 보류 기능 | 본인 시청 계정 로그인 VOD, 공식 API 시청자 수 기록 — 기능 동등 범위에서 제외 |
| 개발 방법 | [anthropics/skills](https://github.com/anthropics/skills)의 스킬을 단계별로 적용 (2절) |

## 2. 적용 스킬 (anthropics/skills)

저장소의 스킬 19개 중 이 프로젝트에 맞는 5개를 적용합니다. 모두 Apache 2.0입니다.

| 스킬 | 적용 위치 | 이 설계에 반영한 내용 |
|---|---|---|
| **claude-api** | `internal/ai` (6.3절, 12절 5단계) | 공식 Go SDK `anthropic-sdk-go`, 구조화 출력 `output_config.format`, 프롬프트 캐싱 순서, 배치 API(50% 비용), 토큰 카운트로 비용 미리보기, 상태 코드별 오류 처리 |
| **mcp-builder** | `internal/mcp` (6.5절) | 수집 자료를 읽기 전용 MCP 도구로 공개해 Claude Desktop·Claude Code에서 직접 분석. 도구 이름 접두어, 페이지네이션, 읽기 전용 표시, 평가 질문 10개 |
| **frontend-design** | `web/` (9절) | 구현 전 디자인 계획(색·서체·레이아웃·원칙) 작성 후 검토, 템플릿 같은 기본값 피하기, 접근성 기준, 화면 문구 원칙 |
| **webapp-testing** | `e2e/` (10절) | Python Playwright 스크립트와 `with_server.py`로 단계별 정상 작동 테스트. 화면 탐색 후 조작하는 순서 |
| **skill-creator** | `.claude/skills/` (11절) | 반복 작업(플랫폼 어댑터 추가, 탐지 규칙 추가, E2E 점검)을 프로젝트 전용 스킬로 정리 |

적용하지 않는 스킬: 문서 생성 스킬(docx, pdf, pptx, xlsx), 디자인·아트 스킬(canvas-design, algorithmic-art, theme-factory, brand-guidelines), web-artifacts-builder(claude.ai 아티팩트 전용), 사내 커뮤니케이션 스킬 등. 이 앱의 기능과 관계가 없습니다.

### 적용 방식

규칙을 문서로만 두지 않고, Claude가 작업할 때 자동으로 따르도록 세 단계로 적용합니다.

| 단계 | 방식 | 위치 |
|---|---|---|
| 1. 외부 스킬 포함 | 위 4개 스킬(claude-api 제외)을 고정 커밋으로 저장소에 포함. 이 저장소를 여는 모든 Claude Code 세션에서 바로 사용 가능 | `.claude/skills/{frontend-design,webapp-testing,mcp-builder,skill-creator}/`, 출처 `.claude/skills/UPSTREAM.md` |
| 2. 규칙 스킬 | 이 설계의 구조·코드 규칙을 Agent Skills 표준 규격(agentskills.io/specification)의 프로젝트 스킬로 작성. 코드 작업 시 description으로 자동 선택됨 | `.claude/skills/momentforge-architecture/`, `.claude/skills/port-legacy-module/` (11절) |
| 3. 자동 검사 | Go 파일을 수정할 때마다 훅이 의존 방향·위치 규칙·gofmt를 검사하고, 위반을 Claude에게 돌려줘 바로 고치게 함 | `.claude/settings.json` PostToolUse 훅 → `check_layers.py --hook` |

claude-api는 Claude Code에 내장되어 있고 모델 변경에 맞춰 갱신되므로 저장소에 넣지 않습니다.

## 3. 기존 구조의 한계

| 영역 | 현재 | 문제 |
|---|---|---|
| 배포 | Python + venv + 설치 스크립트 5종 | 설치 실패 지점이 많고 OS별 검증 부담 |
| 서버 | `server.py`의 `make_server` 안 중첩 핸들러, `if/elif` 경로 분기 | 엔드포인트 추가 시 한 함수가 계속 커짐 |
| 플랫폼 | 치지직 URL·응답 해석이 `transport`/`live_transport`/`comments`에 직접 포함 | 다른 플랫폼 추가 시 전체 수정 |
| 탐지 | `detector.py`에 D1~D5 규칙과 임계값이 하드코딩 | 규칙 추가·민감도 조정이 코드 수정 |
| LIVE | 동시에 1개 작업만 (`self.active`) | 다채널 동시 수집 불가 |
| 성능 | `WindowRules.add`가 배치마다 전체 이벤트 재정렬, 창 통계 캐시는 최근 256개로 제한, 인터프리터 속도 | 장시간 LIVE에서 CPU 낭비 (1단계 측정: 5시간 분석 630ms, Go 109ms) |
| 저장 | VOD·LIVE 저장소가 별도, JSON `body` 컬럼 중심 | 조회·통합 타임라인이 어렵고 중복 코드 |
| AI | OpenAI Responses API 하나에 고정, 표준 라이브러리로 직접 HTTP 호출 | 제공자 교체·자동 분석·비용 관리 불가 |
| UI | 바닐라 JS 3개 파일 + 폴링 | 상태 관리·실시간 갱신이 복잡 |
| 테스트 | 테스트 코드 없음 | 변경 영향 확인 불가 |

## 4. 목표 아키텍처

```text
     Claude Desktop / Claude Code                브라우저
              │ (stdio)                             │
     internal/mcp (읽기 전용 도구)      web (React+TS, embed)
              │                          REST(JSON) │ SSE(실시간)
              │                                     │
  cmd/momentforge ── internal/api (chi 라우터, 미들웨어: 로컬키·Host·Origin)
              └──────────────┬──────────────────────┘
                   internal/app (유스케이스: 수집·분석·내보내기·AI)
        ┌──────────────┬─────┴──────────┬───────────────┬──────────────┐
    platform        detect           store            ai            export
  (플랫폼 어댑터)  (규칙 엔진)     (SQLite 저장소)  (AI 제공자)    (TXT/CSV/JSON)
    chzzk/ ...      rules/ ...      migrations/   anthropic/ openai/
                                     │
                     internal/events (내부 이벤트 버스 → SSE)
```

원칙

- **의존성 방향은 바깥 → 안쪽**: `api`·`mcp` → `app` → (`platform`, `detect`, `store`, `ai`)의 인터페이스. 구현체끼리는 서로 모름.
- **확장 지점은 모두 인터페이스 + 등록(registry)**: 새 플랫폼·규칙·AI 제공자는 패키지 하나 추가 + 등록 한 줄.
- VOD와 LIVE는 **같은 파이프라인**: `채팅 소스 → 저장 → 증분 분석 → 후보`. VOD는 끝이 있는 스트림일 뿐.
- HTTP API와 MCP는 **같은 `app` 유스케이스**를 호출하는 두 개의 입구. 로직을 중복하지 않음.

## 5. 디렉터리 구조

```text
momentforge/
├─ cmd/momentforge/main.go        진입점 (serve, mcp, version)
├─ internal/
│  ├─ api/                        HTTP 라우팅·핸들러·SSE·보안 미들웨어
│  ├─ mcp/                        MCP 서버 (stdio, 읽기 전용 도구)
│  ├─ app/                        작업 관리자(Job), 세션 수명주기
│  ├─ domain/                     공용 타입 (Session, ChatEvent, Candidate, Gap, UserError)
│  ├─ platform/                   Platform 인터페이스·registry
│  │  └─ chzzk/                   치지직 VOD·LIVE·댓글·재생·시청자 수
│  ├─ detect/                     엔진(창 통계·후보 묶기)·Rule 인터페이스
│  │  └─ rules/                   laugh, surge, repeat, emotion, participants
│  ├─ store/                      SQLite(modernc, CGO 없음)·마이그레이션
│  ├─ ai/                         Provider 인터페이스·프롬프트·검증
│  │  ├─ anthropic/               anthropic-sdk-go
│  │  ├─ openai/                  기존 기능 동등용
│  │  └─ opencode/                OpenCode CLI 하네스로 OpenCode Go 모델 사용
│  ├─ export/                     TXT/JSONL/CSV/JSON·checkpoint
│  ├─ events/                     pub/sub 버스
│  └─ privacy/                    작성자 가명화(salt+HMAC)
├─ web/                           Vite + React + TS (embed.go: 빌드 결과를 Go 패키지로 내장)
│  ├─ src/features/{vod,live,timeline,ai,settings}/
│  └─ dist/ → go:embed
├─ e2e/                           Python Playwright 정상 작동 테스트 (개발용)
├─ .claude/
│  ├─ settings.json               구조 검사 훅
│  └─ skills/                     외부 스킬 4개 + 프로젝트 스킬 (2절, 11절)
├─ docs/
│  ├─ REDESIGN.md                 이 문서
│  └─ DESIGN-SYSTEM.md            frontend-design 디자인 계획 (9절)
├─ CLAUDE.md                      Claude Code 개발 지침
└─ Makefile / .goreleaser.yaml    OS별 단일 바이너리 빌드
```

외부 의존성(최소화): `go-chi/chi`(라우팅), `modernc.org/sqlite`(순수 Go SQLite → 크로스 컴파일 용이), `coder/websocket`(LIVE 채팅 WSS), `anthropics/anthropic-sdk-go`(Claude), `modelcontextprotocol/go-sdk`(MCP). 나머지는 표준 라이브러리. `e2e/`의 Python·Playwright는 개발 환경에만 필요하고 배포물에는 포함하지 않음.

## 6. 핵심 인터페이스

### 6.1 플랫폼 어댑터

```go
type Platform interface {
    ID() string                                 // "chzzk"
    Match(rawURL string) (Target, bool)          // URL → VOD/LIVE 대상 판별
    Capabilities() Caps                          // VOD, Live, Comments, Playback, Viewers 지원 여부
}

type VODSource interface {                       // 끝이 있는 채팅 스트림 (이어받기 커서 포함)
    Meta(ctx context.Context, t Target) (VODMeta, error)
    Chats(ctx context.Context, t Target, cursor Cursor) iter.Seq2[ChatPage, error]
}

type LiveSource interface {                      // 끝이 없는 채팅 스트림
    Inspect(ctx context.Context, t Target) (LiveMeta, error)
    Stream(ctx context.Context, t Target, out chan<- ChatEvent) error // 재접속은 app 계층이 담당
}

type CommentSource interface { Comments(ctx context.Context, t Target) ([]Comment, error) }
type PlaybackSource interface { HLS(ctx context.Context, t Target) (PlaybackInfo, error) }
type LivePlaybackSource interface { LivePlayback(ctx context.Context, t Target) (Playback, error) } // 생방송의 지금 방송 (15절 2026-10-17)
type ViewerSource interface { Viewers(ctx context.Context, t Target) (int, error) }
```

- 기능별 인터페이스로 나눠 플랫폼마다 지원 범위가 달라도 됨 (타입 단언으로 확인 → UI에 `Capabilities` 전달).
- `domain.ChatEvent`는 플랫폼 중립: `{PlatformMsgID, AuthorID, Text, OffsetMs, ReceivedAt, Kind(chat/donation/...)}`. 어댑터는 원본 `AuthorID`를 담아 반환만 하고, `app`이 `privacy`로 세션별 가명 키(`AuthorKey`)를 만든 뒤 저장합니다. 원본 ID는 저장하지 않습니다.
- 응답 형식 변경은 `ErrSchemaChanged`로 통일 (기존 `SCHEMA_CHANGED` 동작 유지).

### 6.2 탐지 규칙 엔진 (1단계 구현)

```go
type Rule interface { ID() string; Name() string }      // ID = 신호 키 또는 키 접두어
type MessageRule interface {                              // 메시지마다 반응 키를 붙임
    Rule
    Keys(text string) []string                            // "laugh", "emotion:surprise", "repeat:<문장>"
    Share(p Params) float64                               // 창 작성자 중 필요한 비율
}
type WindowRule interface {                               // 창 전체를 판정
    Rule
    Fires(cur WindowStats, base Baseline, p Params) bool  // "surge", "participants"
}
```

- 엔진(`detect.Engine`, `detect.Grouper`, `detect.Analyze`)이 **10초 창 / 5초 간격 / 기준선 학습 / 공통 조건 / 메시지 규칙의 비율 판정 / 후보 병합·분할 / 편집 구간**을 맡고, 규칙은 판정만 합니다. 규칙 추가 절차는 `add-detection-rule` 스킬.
- 모든 임계값은 `detect.Params`. `DefaultParams()`는 새 분석의 기본값이고, `CzzEditParams()`와 `rules.CzzEdit()`는 기존 결과 비교용으로 고정합니다. 두 값의 차이는 판정 시작 조건 자동 조정(6.2.1)뿐입니다.
- 같은 엔진이 VOD(한 번에 분석)와 LIVE(`Add`, `Gap`, `Evaluate`, `Prune`로 증분 분석)를 처리합니다. 과거 10초 칸 통계는 한 번 계산해 재사용하고, 늦게 도착한 채팅이 들어오면 해당 칸만 다시 계산합니다.
- **기능 동등 확인**: `internal/detect/rules/testdata/legacy/`의 4가지 경우(데모, 데모+민감도, 공백·명령·작성자 누락·자모 분리·180초 분할이 섞인 합성 데이터 2종)에서 후보와 5초 창의 모든 필드가 CzzEdit 0.3.2와 같습니다 (`TestMatchesLegacy`). 비교 데이터는 `gen_cases.py`로 다시 만들 수 있습니다.
- **속도** (5시간, 채팅 52,114건, 같은 입력): CzzEdit 630ms → Go 109ms. 결과는 같습니다.
- 시청자 수 보정(기준선 시작 시점 이동, 기준 전환 시 후보 마감)은 3단계에서 `Evaluate(t, baselineFrom)`과 `Grouper`에 연결합니다.

#### 6.2.1 판정 시작 조건: 채널 규모와 시간대에 맞춘 자동 기준 (3단계, 규칙 2.0.0)

모든 신호는 먼저 "판정 시작 조건"을 넘어야 계산됩니다. CzzEdit는 이 조건을 고정값(10초에 채팅 15건·작성자 5명)으로 두었습니다. 사용자 요청("새벽 4시, 시청자 1000·100·50명 방, 같은 1시간 안의 붐빔과 한산함에 따라 기준이 유동적이어야 한다")으로 아래처럼 분석하고 바꿨습니다.

**1. 무엇이 달라지는가 (실제 데이터 측정)**

| 데이터 | 측정 |
|---|---|
| 2단계 실제 다시보기 (4시간 32분) | 5분 채팅 수가 47~532건으로 한 방송 안에서 **11배** 차이. 최근 15분 채팅한 사람은 21~63명 |
| 3단계 실제 생방송 (시청자 74~83명) | 분당 18건, 최근 15분 채팅한 사람 25~29명. 다시보기와 사람 수는 비슷하지만 1인당 채팅이 절반 |

- 채팅 건수는 시청자 수보다 그 채널 사람들의 채팅 습관에 더 크게 좌우됩니다. 그래서 시청자 수를 직접 기준으로 쓰지 않고, 그 채널의 **평소 채팅량**을 기준으로 씁니다. 다시보기에는 시청자 수 자료도 없습니다.
- 생방송의 시청자 수 보정(시청자가 10% 넘게 바뀌면 평소 수준을 다시 학습)은 그대로 둡니다.

**2. 고정 기준의 문제는 양방향**

같은 다시보기를 작성자 일부만 남기거나 복제해 시청자 규모를 바꿔 보면 다음과 같습니다.
- 작은 방에서는 후보가 사라집니다(작은 생방송 0개, 시청자 50%로 줄인 다시보기 1개).
- 큰 방에서는 폭증합니다(3배로 키운 다시보기 125개, 원래 16개). 큰 방에서는 "웃은 사람 5명" 같은 고정 최소치가 너무 쉽게 넘어가기 때문입니다.

**3. 원칙**
1. **규모**: 기준을 모두 그 채널 평소 값의 배수로 정하고 상한을 두지 않습니다. 같은 내용이면 50명 방이든 1000명 방이든 같은 순간을 고르게 됩니다.
2. **시간 두 척도**:
   - 장기(최근 2시간): 새벽처럼 몇 시간에 걸친 변화를 따라갑니다. 뜨거운 30분을 "평소"로 착각하지 않습니다.
   - 단기(최근 15분): 한산한 시간대 안의 돌출 반응을 잡습니다.
3. **평소 = 중앙값**: 반응이 잦은 방송에서도 반응 자체가 기준을 끌어올리지 않습니다. 상위 분위수를 쓰면 반응 구간이 기준이 되어 다음 반응을 막았습니다(데모·합성 데이터에서 확인).
4. **최소 증거**: 기존 민감도 값(채팅 6건·3명) 아래로는 내려가지 않습니다. 반응한 사람이 4~5명 이하인 방은 어떤 기준으로도 우연과 구분할 수 없습니다.

**4. 규칙 (민감도를 끈 기본값)**

| 항목 | 값 |
|---|---|
| 장기 기준 (방송 흐름 대비 붐빔) | 10초 채팅 수 ≥ 최근 2시간 10초 칸 채팅 수 중앙값 × **3.75** (최소 6건). 작성자·반응 작성자는 기존 비율(15건에 5명) |
| 단기 기준 (갑작스런 반응) | 10초에 채팅한 사람 ≥ 최근 15분 10초 칸 작성자 수 중앙값 × **5** (최소 3명), 채팅 수도 그 이상. 반응 작성자는 3명 이상 |
| 판정 시작 | 둘 중 **하나**만 넘으면 됩니다 |
| 처음 3분 (18칸) | 채널을 지켜보기만 하고 판정하지 않습니다. 고정 기준을 쓰면 큰 방에서 시작 직후 후보가 몰렸습니다 |
| 민감도를 켜면 | 지금처럼 항상 6건·3명 |
| 그대로 두는 것 | 웃음 35%·평소 대비 15%포인트, 급증 3배, 후보 묶기·분할 |

- 3.75는 실제 다시보기의 평소 10초 채팅 수(중앙값 4건)에서 기존 15건이 갖던 배수입니다.
- 5는 시험에서 한산한 구간의 돌출 반응을 잡으면서 규모 일관성을 지킨 값입니다.
- 공백 구간과 최근 40초는 평소 값 계산에서 뺍니다.
- 그때의 두 기준은 창과 후보 정점의 `gate`(장기, 채팅 수)와 `spikeGate`(단기, 사람 수)에 남아 화면에 표시됩니다.

**5. 시험 방법과 결과**

실제 다시보기의 작성자를 해시로 일부만 남겨, 같은 방송을 여러 조건으로 다시 만들었습니다.
- 시청자 50%·30%
- 처음 100%에서 끝 25%로 줄어드는 새벽형
- 30분마다 100%와 30%를 오가는 방송
- 작성자를 바꿔 3배로 복제한 큰 방
- 작은 생방송을 10배로 복제한 방

실제 채팅은 저장소에 넣지 않았습니다. 아래는 실제 엔진 코드로 낸 결과입니다(엔진 사본에서 비교한 단계별 방식은 결정 기록 참고).

| 방식 | 다시보기 후보 | 3배로 키운 방 | 50%로 줄인 방 | 작은 생방송 / 10배 | 반응 잦은 데모·합성 |
|---|---|---|---|---|---|
| 고정 15건 (CzzEdit) | 16 (시간당 3.5) | 125개 (폭증) | 원래 후보 중 1/16 | 0 / 13 | 3 / 7 |
| 전체 기간 상위 3%, 최대 15건 (앞 설계) | 18 | 125개 (폭증) | 12/18 | 4 / 13 | 같음 |
| 단기 기준만 (15분 작성자 ×4) | 17 | 같은 순간 16/17 | 10/17 | 2 / 4 | — |
| 장기·단기 결합, 상위 분위수 | 28 | 27/28 | 20/28 | 4 / 6 | 혼합 7개 중 1개 놓침 |
| **장기·단기 결합, 중앙값 (채택)** | **24 (시간당 5.3)** | **같은 순간 21/24** | **17/24** | **2 / 4** | **같은 순간·이유** |

- 작은 생방송에서는 사용자가 본 ㅋㅋ 반응 두 곳(3:14:30, 3:24:20)만 찾습니다. 10배로 키워도 4개입니다(고정 기준은 13개).
- 데모·합성 혼합은 후보의 시작 시각과 이유가 기존과 같습니다. 반응이 잦아드는 마지막 5초가 빠지는 경우만 있습니다.
- 다시보기에서 고르는 순간이 바뀝니다.
  - 기존 16개 중 8개는 그대로입니다.
  - 붐비던 0:30~1:20의 중간 강도 반응 8개(채팅 15~21건)는 빠집니다. 그 시간대 장기 기준이 19~23건이었습니다.
  - 한산해진 2:10~4:20과 시작 직후에 평소의 4~6배로 사람이 몰린 순간 16개가 새로 잡힙니다.
  - 상대 기준을 쓰는 한 "그 시간대치고 큰 반응"을 고르게 되므로 생기는 차이입니다.

**6. 한계와 조정**
- 1000명 이상 큰 방송의 실제 채팅으로는 확인하지 못했습니다(복제로만 재현). 실제 큰 방송으로 확인이 필요합니다.
- 시청자가 매우 적어 반응한 사람이 4~5명 이하면 찾지 못합니다(최소 증거).
- 규칙 안의 고정 최소치 일부(참여자 급증 8명, 강한 창 30건·10명)는 그대로입니다.
- 조정할 값:
  - `GateBusyRatio`(3.75): 낮추면 붐비는 시간대 후보가 늘어납니다. 3이면 다시보기 후보가 약 2배입니다.
  - `GateSpikeRatio`(5): 낮추면 한산한 시간대 후보가 늘어납니다.
  - `GateLookbackMs`(2시간): 늘리면 새벽 변화를 늦게 따라갑니다.
  - 아주 작은 반응까지 보려면 "항상 가장 민감하게"를 켭니다.

**구현.**
- `detect.Params`에 `GateBusyRatio`·`GateLookbackMs`·`GateWarmupBins`·`GateSpikeRatio`를 추가했습니다. `CzzEditParams()`는 0이라 기존 비교 테스트가 그대로 통과합니다.
- `Engine.finishBins`가 확정된 10초 칸(최근 40초 경계를 지난 칸, 공백 제외)의 채팅 수를 2시간 동안 정렬해 두고, `Engine.gate`가 두 기준을 계산합니다.
  - LIVE는 분석 완료 시점보다 앞선 채팅을 분석에 넣지 않으므로 확정 칸이 바뀌지 않습니다. 그래서 증분 분석과 일괄 분석의 결과가 같습니다.
- 화면: 후보 설명에 "그때 판정 시작 기준: 채팅 N건 또는 M명"을, LIVE 수집 상태에 두 기준을 표시합니다. 민감도 옵션 이름은 "항상 가장 민감하게"입니다.
- 테스트(`internal/detect/rules/gate_test.go`):
  - 작은 채널 웃음 검출
  - 학습 3분 동안 판정하지 않음·최소치 유지
  - 10배 규모에서 같은 순간
  - 새벽형 감소 뒤 반응 검출(고정 기준은 놓침)
  - 반응 잦은 데모·합성의 후보 유지
  - LIVE 증분 = 일괄 분석
- 이미 저장한 분석과 이 변경 전에 시작한 LIVE 기록은 저장된 설정(고정 기준)을 그대로 씁니다.

### 6.3 AI 제공자 (확장 1순위, claude-api 스킬 적용)

5단계 구현 기준입니다. 제공자는 보내기만 하고, 보낼 내용(`ai.Prepare`)과 받은 내용 검증(`ai.Validate`)은 제공자와 무관한 `internal/ai`가 맡습니다.

```go
type Provider interface {
    ID() string; Name() string
    KeyEnv() string              // 시작할 때 읽는 키 환경변수 (ANTHROPIC_API_KEY 등)
    DefaultModel() string        // 화면에 미리 채우는 모델 ("" 이면 사용자가 입력)
    MaxOutputTokens() int        // 요청 하나의 출력 상한 (사고 포함)
    Review(ctx context.Context, key, model string, chunk Chunk) (json.RawMessage, Usage, error)
}

// 선택 기능: 지원하는 제공자만 구현하고, app이 타입 단언으로 확인
type Priced interface{ Price(model string) (Price, bool) }                       // 공시 가격
type TokenCounter interface{ CountTokens(ctx, key, model string, c Chunk) (int, error) }
type ModelLister interface{ Models(ctx, key string) ([]Model, error) }
type BatchProvider interface {
    SubmitBatch(ctx, key, model string, reqs []BatchRequest) (batchID string, err error)
    BatchStatus(ctx, key, batchID string) (BatchStatus, error)
    BatchResults(ctx, key, batchID string) ([]BatchResult, error) // 요청 ID로 맞춤, 순서 보장 없음
    CancelBatch(ctx, key, batchID string) error
}
```

`Usage`는 입력을 가격별로 나눠 둡니다(캐시 없음·캐시 읽기·캐시 쓰기, 출력). 제공자가 비용을 직접 알려 주면 `CostUSD`에 담고, app은 가격표가 없을 때 그 값을 씁니다. Claude는 네 가지 선택 기능을 모두 구현하고, OpenAI는 기존 기능(바로 보내기)만 구현합니다.

```go
// 다른 프로그램의 로그인을 쓰는 제공자: MomentForge는 키를 받지 않고 ""를 넘김
type SelfAuthenticated interface {
    Ready() error     // 그 프로그램을 쓸 수 있으면 nil
    AuthHelp() string // 로그인 방법 (화면 안내)
}
```

OpenCode Go (`internal/ai/opencode`): OpenCode Go 구독 모델(`opencode-go/…`, 목록은 `opencode models`)을 **OpenCode CLI를 하네스로** 씁니다. Go 공식 문서가 직접 호출하는 프로그램에 "일반적인 코딩 에이전트 트래픽, 자체 User-Agent, 대화별 `x-opencode-session`"을 요구하므로, API를 직접 부르지 않고 OpenCode 자체가 요청을 보내게 합니다. 키는 OpenCode의 로그인(`/connect`)을 쓰고 MomentForge는 받지 않습니다(`SelfAuthenticated`).

| 항목 | 규칙 |
|---|---|
| 실행 | 요청마다 빈 임시 폴더에서 `opencode run --standalone --format json --agent momentforge-review -m <모델>`. 공유 백그라운드 서버 대신 개인 서버를 써서 폴더의 설정이 적용되게 함 |
| 도구 차단 | 그 폴더의 `opencode.json`이 전역과 에이전트 권한을 모두 `{action:"*", resource:"*", effect:"deny"}`로 두고, 에이전트는 `steps: 1`, 지시문은 `ai.Prompt`. 결과에 완료된 도구 실행이 있으면 답을 쓰지 않음(`AI_TOOL_USE`) |
| 입력 | 지시·스키마·입력 JSON을 표준 입력으로 넘김(명령줄에 채팅을 두지 않음). 표준 입력을 닫지 않으면 CLI가 기다리므로 항상 내용을 넘기고 닫음 |
| 출력 | JSON 줄 이벤트에서 `text`의 `part.text`를 모아 JSON 객체를 꺼내고(코드 블록 허용) `Validate`로 확인. `step_finish`의 `part.tokens`·`part.cost`를 사용량으로 기록. 오류 이벤트는 두 세대 형식(`{type,message,status}`, `{name,data:{message,statusCode}}`)을 모두 읽음 |
| 정리 | 끝나면 `opencode session delete --standalone <세션>`으로 OpenCode 기록에서 지우고 임시 폴더를 삭제 |
| 재시도 | 429·5xx만 재시도. 구독·인증(401·403)·모델 없음은 OpenCode 문구를 그대로 보여 주고 멈춤. 10분 안에 끝나지 않거나 결과를 모르면 재전송하지 않음 |
| 찾기 | `PATH`, 그다음 `~/.local/bin`, `~/.opencode/bin`, `/opt/homebrew/bin`, `/usr/local/bin`. 환경변수로 실행파일을 바꾸는 기능은 두지 않음 |
| 미지원 | 토큰 계산 API·배치·가격표가 없어 비용 한도·자동 선택·배치를 쓸 수 없음. 비용은 OpenCode가 알려 준 값으로 표시 |

Claude 구현 규칙 (claude-api 스킬 기준, `internal/ai/anthropic`):

| 항목 | 규칙 |
|---|---|
| SDK | `github.com/anthropics/anthropic-sdk-go`. 표준 라이브러리로 직접 HTTP를 호출하지 않음. 클라이언트는 환경 기본값을 끄고(`ANTHROPIC_BASE_URL`·프로필 무시) 사용자가 준 키만 쓰며, SDK 자동 재시도도 끔(재시도는 app이 횟수를 세며 함) |
| 기본 모델 | `claude-opus-5-5`. 모델 ID 상수와 가격표는 이 패키지에만 둠. 화면의 모델 목록은 Models API로 조회 |
| 사고·effort | 사고는 모델 기본값(현재 모델은 적응형)을 그대로 씀. effort는 항상 `high`로 명시(Opus 5.5 기본값은 `medium`). `budget_tokens`·사고 비활성화는 쓰지 않음 (400 오류). 사고가 출력 상한을 함께 쓰므로 요청당 출력 상한은 8,000 |
| 출력 형식 | 구조화 출력 `output_config.format`(JSON 스키마). prefill과 `tool_choice` 강제는 쓰지 않음 |
| 프롬프트 캐싱 | 고정 지시문(`ai.Prompt`, 2판)에 `cache_control`. 지시문에 시각·무작위 값이 없고 1,200자 이상이라 Claude의 캐시 최소 길이(512토큰)를 넘음(`TestPromptIsCacheable`). 보낼 세션 공통 자료가 없어 "세션 공통 자료" 층은 두지 않음. `cache_read_input_tokens`를 실행마다 기록 |
| 긴 요청 | 요청은 10KB 이하·출력 8,000토큰 이하라 스트리밍 없이 보냄(SDK가 출력 상한에 맞춰 시간 제한을 정함) |
| 미리보기 | 보낼 내용과 **최대 비용**을 항상 표시: 바이트 기반 입력 상한 × 공시 가격(입력은 캐시 쓰기 가격, 출력은 상한까지). 토큰 카운트 API로 정확한 입력 토큰을 세는 일은 사용자가 미리보기를 본 뒤 누를 때만 함(카운트 요청에도 채팅이 전송되므로) |
| 자동 분석 | "모든 후보 자동 선택" + 비용 한도(필수) + 미리보기 확인 후 실행. 배치 API(가격 50%)를 고를 수 있음. 요청 ID는 계획 안의 순번 `chunk-NNN`(요청 하나가 후보 하나가 아니라 시간 창이라서) |
| 비용 한도 | 미리보기에서 최대 비용이 한도를 넘는 요청은 "비용 한도"로 빼고, 바로 보내기 중에는 실제 비용 + 다음 요청 최대 비용이 한도를 넘으면 멈춤(`COST_CAP`). 가격을 모르는 모델은 한도·자동 선택을 쓸 수 없음 |
| 오류 처리 | `errors.As`로 `*anthropic.Error`를 꺼내 상태 코드별 분기: 429·5xx·529와 전송 전 연결 실패는 재시도, 400·401·403·404는 즉시 실패 후 원인 표시. 전송 뒤 결과를 모르면 중복 비용을 막으려 재전송하지 않음 |
| 거절 | `stop_reason: refusal`은 `AI_REFUSAL`로 표시. 서버 측 대체 모델(fallbacks)은 켜지 않음: 미리보기에서 확인한 모델이 아닌 모델로 채팅이 전송되기 때문 |
| 인증 | API 키는 서버 메모리에만 보관. 환경변수 `ANTHROPIC_API_KEY`가 있으면 사용. 파일·DB·로그에 쓰지 않음 |

공통 흐름은 유지: **미리보기(전송 내용·최대 비용) → 사용자 확인 → 실행**. 수집이 끝나면 확인 없이 저절로 보내는 자동 분석은 하지 않습니다(15절). 생방송 기록도 같은 흐름이며, 수집 중에는 미리보기를 만든 순간까지 저장된 채팅과 확정된 후보만 계획에 넣습니다(15절).

배치 실행: 접수한 배치 ID를 `ai_runs`에 저장하고 30초부터 최대 5분 간격으로 결과를 확인합니다. 앱을 끄면 확인만 멈추고 배치는 계속 처리되며, 다음 실행 때 키가 메모리에 있으면(환경변수 또는 화면에서 다시 저장) 결과를 가져옵니다. 결과를 기다리는 배치가 있어도 다른 AI 실행을 할 수 있고(최대 5개), 바로 보내기는 한 번에 하나입니다.

결과 비교: 같은 세션의 두 실행을 고르면 시간이 가장 많이 겹치는 추천끼리 1:1로 짝짓고, 한쪽에만 있는 추천을 따로 보여줍니다. 두 실행이 같은 채팅을 보냈는지도 표시합니다. 화면은 편집 프로그램의 두 트랙처럼 그립니다.

### 6.4 작업(Job) 관리

```go
type JobManager interface {
    Start(ctx context.Context, spec JobSpec) (JobID, error)  // VOD 수집, LIVE 수집, 재분석, 내보내기, AI
    Stop(id JobID) error
    List() []JobStatus
}
```

- 작업마다 goroutine + `context` 취소. **LIVE 다채널 동시 수집**을 기본 구조로 지원 (채널당 수신 goroutine 1 + 저장 goroutine 1, 유한 채널 버퍼).
- 기존 보장 유지: 큐 상한 10,000, 약 1초/200건 단위 배치 저장, 지수 백오프 재접속(최대 60초, 약 10분 실패 시 사용자 재개 대기), 과부하를 정상 종료로 숨기지 않음.
- 진행 상황은 `events` 버스로 발행 → `/api/events` SSE로 UI에 push (폴링 제거).

### 6.5 MCP 서버 (mcp-builder 스킬 적용)

`momentforge mcp` 명령으로 stdio MCP 서버를 실행합니다. Claude Desktop이나 Claude Code에 등록하면 수집한 채팅과 후보를 Claude가 직접 조회해 분석할 수 있습니다. 앱 안의 AI 기능(6.3절)과 별개로, API 키 없이 사용자의 Claude 구독으로 분석하는 경로입니다.

| 원칙 | 내용 |
|---|---|
| SDK | mcp-builder는 TypeScript를 권장하지만, 단일 실행파일 유지를 위해 공식 Go SDK(`modelcontextprotocol/go-sdk`) 사용 |
| 전송 방식 | 로컬 전용이므로 stdio |
| 도구 범위 | **읽기 전용**만 제공. 수집 시작·삭제 같은 쓰기 도구는 제공하지 않음 (`readOnlyHint: true`) |
| 이름 | `mf_` 접두어 + 동작: `mf_list_sessions`, `mf_get_session`, `mf_search_chats`, `mf_get_chats_around`, `mf_list_candidates`, `mf_get_candidate_context`, `mf_get_timeline` |
| 응답 | 구조화 출력(`outputSchema`) + 사람이 읽을 텍스트. 채팅 조회는 `limit`·`cursor` 페이지네이션 필수 (긴 방송은 수만 건) |
| 오류 | 다음 행동을 알려주는 문구 (예: "세션 ID가 없습니다. mf_list_sessions로 ID를 확인하세요") |
| 개인정보 | 가명 작성자 키만 노출. 원문 응답(`raw_pages`)은 제공하지 않음 |
| 평가 | 데모 데이터 기준 질문 10개를 `e2e/mcp-eval.xml`로 작성 (스킬의 평가 형식) |

5단계 구현: `momentforge mcp --data <데이터 폴더>`. DB를 SQLite 읽기 전용(`mode=ro`, `query_only`)으로 열어 `momentforge serve`가 수집하는 동안에도 쓸 수 있고, 서버 락·마이그레이션을 건드리지 않습니다(DB 버전이 다르면 안내 후 종료). 키를 읽지 않고 AI 제공자도 등록하지 않습니다.

| 도구 | 입력 (모두 `response_format`: markdown·json) | 돌려주는 것 |
|---|---|---|
| `mf_list_sessions` | `include_hidden`, `limit`(20/100), `cursor` | 세션 ID·제목·종류·길이·채팅 수 |
| `mf_get_session` | `session_id` | 출처별 후보 수, 수집 공백, 영상 댓글 요약, AI 검토 기록(토큰·캐시 적중·비용) |
| `mf_search_chats` | `query`(부분 일치)·`author`(가명) 중 하나 이상, `start`, `end`, `limit`(50/200) | 일치 채팅과 전체 수 |
| `mf_get_chats_around` | `time`, `window_seconds`(30, 최대 600), `limit`(100/200) | 앞뒤 채팅 (영상 길이 안으로 자름) |
| `mf_list_candidates` | `source`(all·rule·viewer_comment·ai), `limit`(50/200) | 편집 구간, 규칙·최고점 통계·근거 수, AI 실행 ID, 겹치는 다른 출처 후보 |
| `mf_get_candidate_context` | `candidate_id`, `limit`(100/200) | 후보와 편집 구간 채팅 (근거 표시) |
| `mf_get_timeline` | `bucket_seconds`(60), `start`, `end`, `limit`(100/500) | 구간별 채팅·작성자 수, 가장 많은 5구간, 공백 |

평가: 데모 데이터에 붙여넣은 시청자 댓글 3개와 결정적인 가짜 AI 검토를 더한 데이터(`cmd/momentforge/mcp_test.go`의 `buildEvalData`)로 질문 10개를 만들고, `TestMCPEvalAnswers`가 도구 호출만으로 답을 다시 구해 확인합니다(`make test`에 포함). 모델로 푸는 평가는 15절 기록 참고.

## 7. 데이터 모델 (SQLite, 새 스키마 v1)

| 테이블 | 주요 컬럼 | 비고 |
|---|---|---|
| `sessions` | id, platform, kind(vod/live), target_id, title, state, config_json, salt, hidden, created_at | VOD·LIVE 통합 |
| `chat_events` | session_id, seq, offset_ms, received_at, author_key, text, platform_msg_id | 인덱스 (session_id, offset_ms) |
| `raw_pages` | session_id, cursor, body | VOD 이어받기·진단용 (선택 보관) |
| `gaps` | session_id, start_ms, end_ms, reason | 연결 공백 — 학습 제외 |
| `analyses` | id, session_id, params_json, created_at, boundary_ms | 재분석마다 1행 |
| `candidates` | analysis_id, id, start_ms, end_ms, rules, score, state(생성중/대기/확정/부분), evidence_ids | |
| `comments` | session_id, offset_ms, text, source | 시청자 추천 |
| `viewer_samples` | session_id, at, count, source | 현재는 공개 웹 시청자 수만. `source`는 이후 공식 API 재개에 대비 |
| `ai_runs` | id, session_id, provider, model, fingerprint, state, delivery(single/batch), batch_id, request_count, plan_json, recommendations_json, usage_json, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, cost_usd, cost_cap_usd | 비용·캐시 적중 기록 (0005, 0006) |

- WAL 모드, 쓰기는 작업별 단일 writer goroutine, 읽기는 커넥션 풀. MCP 서버는 읽기 전용 연결로 같은 DB를 엶.
- 마이그레이션은 `store/migrations/NNNN_*.sql` + `PRAGMA user_version`.
- DB가 원본 기준이고 TXT는 DB에서 재생성 가능하다는 원칙 유지.
- 데이터 폴더: 실행한 위치의 `.momentforge/` (`--data`로 변경). 하위에 `data/`(DB·백업), `exports/`, `runtime/`(접근 키, 서버 락).

## 8. HTTP API

5단계까지 구현된 경로입니다. 모든 `/api` 요청은 접근 키 헤더가 필요하고, 쓰기 요청 본문은 16KB 이하 JSON입니다.

| 메서드·경로 | 용도 | 단계 |
|---|---|---|
| `GET /api/status` | 버전, 스키마, 지원 플랫폼 | 0 |
| `GET /api/events` (SSE) | `ready`, `job`(작업 진행), `overflow`, `live`(LIVE 저장마다: 상태·분석 완료 시점·후보 revision·새 채팅 최대 200건) | 0, 3 |
| `GET /api/job`, `POST /api/job/cancel` | 현재 작업 조회·중단 | 2 |
| `POST /api/collect` `{url, sensitive}` | 다시보기 수집 시작 (202) | 2 |
| `POST /api/demo` `{sensitive}` | 샘플 세션 생성·분석 (201) | 2 |
| `GET /api/sessions`, `GET /api/sessions/{id}` | 저장한 작업 목록·상세 (salt·원문 제외) | 2 |
| `POST /api/sessions/{id}/resume`, `/analyses` | 이어받기, 다시 분석 (202) | 2 |
| `GET /api/sessions/{id}/chats?at=&analysis=` | 재생 위치 앞뒤 30초 채팅 (앞뒤 150건씩) | 2 |
| `GET /api/sessions/{id}/playback` | 공개 HLS 주소: 다시보기 영상, 생방송 수집이면 그 채널의 지금 방송 (저장하지 않음) | 2 |
| `GET /api/sessions/{id}/files`, `/files/*` | 내보내기 파일 목록·다운로드 | 2 |
| `GET /api/analyses/{id}` | 분석 결과 (창, 후보, 근거 요약, 플랫폼 링크) | 2 |
| `GET /api/analyses/{id}/candidates/{cid}/chats?offset=` | 후보 편집 구간 채팅 (근거 표시, 페이지) | 2 |
| `POST /api/live/inspect` `{url}` | 생방송 확인 (제목, 방송 중 여부, 시청자 수) | 3 |
| `POST /api/live` `{url, sensitive, viewerAdaptive}` | LIVE 수집 시작 (201). 한 번에 하나, 같은 채널이면 진행 중인 기록 반환 | 3 |
| `GET /api/live/{id}` | LIVE 상태 (연결 번호, 분석 완료 시점, 시청자 수·보정 상태, 공백, 늦게 받은·중복 채팅 수) | 3 |
| `POST /api/live/{id}/stop`, `/resume` | 중단, 같은 방송 이어 기록 | 3 |
| `GET /api/live/{id}/chats?after=` | 최근 300건, 또는 seq 이후 최대 1,000건 | 3 |
| `GET /api/live/{id}/context?at=` | 지정 시각 앞뒤 30초 채팅 (최대 300건) | 3 |
| `GET /api/live/{id}/density` | 구간별 채팅 수 (5초 이상, 약 1,800칸) | 3 |
| `GET /api/live/{id}/candidates?after=`, `/candidates/{cid}/chats` | revision 이후 바뀐 후보, 후보 편집 구간 채팅 | 3 |
| `POST /api/live/{id}/checkpoint` | 현재까지 저장 (`checkpoints/<시각>/`) | 3 |
| `POST /api/sessions/hide`, `/restore` | 저장 목록 전체 숨김·다시 표시 (데이터는 그대로) | 4 |
| `POST /api/comments` `{sessionId? \| url?, text?}` | 영상 댓글 읽기 또는 붙여넣기 (작업, 202). 주소만 주면 댓글만 받은 작업을 만들거나 같은 영상의 작업을 다시 씀 | 4 |
| `GET /api/sessions/{id}/comments` | 최근 댓글 수집 요약, 시청자 추천, 확인이 필요한 댓글 | 4 |
| `GET /api/sessions/{id}/timeline` | 통합 타임라인 (자동 탐지·시청자 추천·AI 추천, 시각순). LIVE는 자동 탐지 후보와 AI 추천 | 4 |
| `GET /api/ai`, `POST /api/ai/key` | AI 제공자와 키 설정 여부, 키를 메모리에 저장·삭제 (응답에 키를 돌려주지 않음) | 4 |
| `POST /api/ai/preview` | 보낼 내용 전체(지시문·구간·채팅)와 추정치. 아무것도 보내지 않음, 10분 뒤 만료 | 4 |
| `POST /api/ai/runs` `{previewId, fingerprint}`, `GET /api/ai/runs/{id}`, `POST …/cancel` | 확인한 미리보기만 실행, 진행·결과(토큰·캐시 적중·비용), 중단(배치는 제공자에서 취소). SSE `ai` 이벤트 | 4, 5 |
| `GET /api/ai` | 제공자별 기본 모델과 지원 기능(토큰 계산·모델 목록·배치·가격) | 5 |
| `POST /api/ai/models` `{provider}` | 키로 쓸 수 있는 모델 목록 (키만 보냄) | 5 |
| `POST /api/ai/preview` `{…, mode: candidates\|all\|range, delivery: single\|batch, costCapUsd}` | 미리보기에 가격·최대 비용·비용 한도로 뺀 요청 포함 | 5 |
| `POST /api/ai/preview` `{sessionId: <LIVE 기록>, …, toEnd}` | 생방송 기록도 같은 세 방식. 후보는 확정·부분 문맥 마감만, 구간은 분석 완료 위치까지(`toEnd`: 끝을 서버의 분석 완료 위치로), 채팅은 미리보기 순간의 최대 seq까지 (15절) | 6+ |
| `POST /api/ai/preview/count` `{previewId, fingerprint}` | 정확한 입력 토큰과 최대 비용. 미리보기의 채팅을 제공자 토큰 계산 API로 보냄(사용자가 누를 때만) | 5 |
| `GET /api/sessions/{id}/ai/runs`, `GET /api/ai/compare?a=&b=` | 세션의 AI 실행 목록, 두 실행 비교(겹침·한쪽에만 있는 추천) | 5 |

보안: 127.0.0.1 바인딩, 실행 시 생성한 로컬 접근 키, Host·Origin 검사 (기존 동작 유지). 접근 키는 `.momentforge/runtime/access-key`에도 기록해 E2E 스크립트가 읽을 수 있게 함.

## 9. 프론트엔드 (frontend-design 스킬 적용)

기술 구성

- Vite + React + TypeScript(strict), 서버 상태는 TanStack Query, 로컬 상태는 React 기본 기능.
- SSE 구독 훅 하나(`useEvents`)로 LIVE 채팅·후보·진행률 갱신.
- LIVE 채팅 목록은 가상 스크롤.
- HLS 재생은 `hls.js` npm 의존성으로 교체 (vendor 파일 제거).
- 화면 구성: 앱 틀(왼쪽 작업 목록) 안에 새 작업(다시보기·생방송 탭) / 다시보기 작업대(영상·채팅·타임라인 + 후보·AI 검토·댓글·파일 탭) / 생방송 작업대(실시간 채팅·타임라인 + 후보·AI 검토·수집 상태·파일 탭). 2026-10-17 개정, 자세한 배치는 `docs/DESIGN-SYSTEM.md` 4절.

디자인 진행 순서 (스킬의 "계획 → 검토 → 구현 → 자체 점검" 절차)

1. **주제 정의**: 방송 편집자가 긴 방송에서 잘라낼 순간을 고르는 작업 도구. 영상 옆에 오래 띄워 두고 쓰는 화면이며, 핵심 정보는 시간축 위의 반응 밀도.
2. **디자인 계획**을 `docs/DESIGN-SYSTEM.md`에 작성: 색 4~6개(이름·hex), 서체와 역할, 레이아웃 와이어프레임(ASCII), 고유 원칙.
3. 계획을 검토해 템플릿 같은 기본값이 있으면 고침. 스킬이 경고하는 기본값: 똑같은 둥근 카드와 회색 그림자의 반복, 모든 제목 위 대문자 라벨, 크림색 배경 + 테라코타 강조색, 검정 배경 + 형광 강조색, 이유 없는 번호 표시, 장식용 그라데이션.
4. 구현 후 스크린샷으로 자체 점검. **눈에 띄는 요소는 하나만**. 이 앱에서는 반응 밀도를 보여주는 **타임라인**이 그 후보.

품질 기준 (따로 강조하지 않고 기본으로 지킴)

- 좁은 화면 대응, 키보드 포커스 표시, `prefers-reduced-motion` 존중, 충분한 명도 대비, 라이트·다크 모드.
- 움직임은 사용자 동작에 대한 응답에만 사용 (후보 선택 시 영상 이동 등).

화면 문구 원칙

- 기능 이름이 아니라 사용자가 하는 일로 표현: "WebSocket 재연결"이 아니라 "연결이 끊겨 다시 연결하는 중".
- 버튼은 결과를 말함: "수집 시작", "후보 저장". 같은 동작은 흐름 전체에서 같은 이름 사용.
- 오류는 무슨 일이 생겼고 어떻게 고치는지 설명. 빈 화면은 다음 행동을 안내.

테스트를 고려한 마크업 (10절과 연결)

- 버튼·입력에 역할(role)과 이름을 정확히 붙여 Playwright가 `get_by_role`로 찾을 수 있게 함.
- 초기 데이터 로딩이 끝나면 `<body data-app-state="ready">`를 설정. SSE 연결이 계속 열려 있어 `networkidle`을 기다릴 수 없기 때문.

## 10. 정상 작동 테스트 (webapp-testing 스킬 적용)

각 단계가 끝나면 아래 두 종류를 실행합니다. 구현 전에 테스트를 먼저 만들지는 않습니다.

| 종류 | 도구 | 범위 |
|---|---|---|
| Go 테스트 | `go test ./...` | 탐지 규칙·엔진, 저장소, 마이그레이션, 플랫폼 응답 파싱(저장한 응답 파일 사용) |
| E2E | Python Playwright (`e2e/`) + 스킬의 `with_server.py` | 실제 실행파일을 띄워 브라우저 흐름 확인 |

E2E 실행 방식

```bash
python e2e/with_server.py --server "./bin/momentforge-e2e serve --no-browser --port 18765 --data e2e/.data" --port 18765 -- python e2e/run.py --base-url http://127.0.0.1:18765
```

- `with_server.py`는 webapp-testing 스킬에서 `e2e/`로 가져와 사용 (Apache 2.0, 출처 표기). 처음 쓸 때 `--help`로 사용법을 확인.
- 스크립트 순서: 페이지 열기 → `data-app-state="ready"` 대기 → 스크린샷·DOM 확인 → 찾은 선택자로 조작. Chromium은 항상 headless.
- 콘솔 오류를 수집해 오류가 있으면 실패 처리.
- 외부 서비스(치지직·AI)는 기본 테스트에서 호출하지 않음. 데모 데이터와 저장한 응답으로 확인하고, 실제 URL 확인은 단계 완료 기준에서 별도로 수행.

## 11. 프로젝트 전용 스킬 (skill-creator 스킬 적용)

프로젝트 스킬은 Agent Skills 표준 규격을 따르며, `skills-ref` 검증 규칙(이름·디렉터리 일치, description 1024자 이하 등)과 skill-creator의 `quick_validate.py`를 통과해야 합니다.

작성 완료:

| 스킬 | 언제 적용되나 | 내용 |
|---|---|---|
| `momentforge-architecture` | 이 저장소의 모든 코드 작성·수정·리팩토링·리뷰 | 작업 절차, 의존 방향 표, 등록 방식, 위치 규칙, 영역별 상세 규칙(`references/go.md`, `web.md`, `ai.md`, `privacy-security.md`), 검사 스크립트 `scripts/check_layers.py` |
| `e2e-check` | 단계 완료·커밋 전, 동작 확인 요청, E2E 실패 진단·추가 | `make test` → `make e2e` → 스크린샷 점검 → 정해진 형식으로 보고 (0단계에서 작성) |
| `add-detection-rule` | 탐지 규칙 추가·임계값 조정, 탐지 결과 설명 | 메시지/창 규칙 선택, Params, `rules.All()` 등록, RulesVersion, 고정된 기존 비교 유지 (1단계에서 작성) |
| `add-platform` | 플랫폼 추가, 플랫폼 응답 변경 대응 | 인터페이스 선택, 공용 HTTP 클라이언트, 응답 검증·PageError, 등록, 가짜 서버 테스트 (2단계에서 작성) |
| `port-legacy-module` | 기존 Python 기능을 새 구조로 옮길 때 | 기존 동작 목록 작성 → 새 위치 결정(`references/legacy-map.md`) → 구현 → 기존 결과와 비교 → 체크리스트 갱신 |

예정 (해당 인터페이스가 실제로 생긴 뒤 작성):

| 스킬 | 만드는 시점 | 내용 |
|---|---|---|

작성 규칙: `SKILL.md`는 500줄 이하, 자세한 자료는 `references/`, 반복 실행 코드는 `scripts/`. 설명(description)에 언제 써야 하는지를 구체적으로 적음.

## 12. 단계별 계획

각 단계 끝에 **정상 작동 테스트(10절)**를 수행하고 통과해야 다음 단계로 넘어갑니다.

| 단계 | 내용 | 적용 스킬 | 완료 기준 |
|---|---|---|---|
| **0. 골격** ✅ | Go 모듈, chi 서버, 보안 미들웨어, SQLite·마이그레이션, Vite 앱 embed, 빌드 스크립트, `e2e/` 기반, `docs/DESIGN-SYSTEM.md` | frontend-design, webapp-testing, skill-creator(`e2e-check`) | 단일 바이너리 실행 → 브라우저에 앱 표시, E2E 1개 통과 |
| **1. 탐지 엔진** ✅ | 엔진 + 규칙 5종 + Params, 데모 데이터 | skill-creator(`add-detection-rule`) | 데모 데이터로 후보 생성, 기존 Python과 결과 비교 확인 |
| **2. VOD** ✅ | 치지직 VOD 메타·채팅 수집·이어받기, 분석, 후보 화면, HLS 재생·채팅 동기화, 내보내기 | frontend-design, webapp-testing, skill-creator(`add-platform`) | 실제 VOD URL로 수집 → 후보 클릭 → 해당 시각 재생 |
| **3. LIVE** ✅ | 치지직 LIVE WSS, 재접속·공백 기록, 증분 분석, checkpoint, 웹 시청자 수 보정, SSE | webapp-testing | 실제 생방송 30분 이상 수집, 중단·이어 기록 확인 |
| **4. 부가 기능** (구현 완료, 실제 확인 대기) | 영상 댓글 추천, 통합 타임라인, 숨김/복원, 선택형 AI (기존과 같은 OpenAI) | webapp-testing | 기존 README 기능표 전 항목 동작 |
| **5. AI 강화 (확장 1순위)** (구현 완료, 실제 확인 대기) | Claude 제공자(6.3절)를 기본으로 전환, 비용 미리보기, 배치 자동 분석·비용 한도, 결과 비교 UI, MCP 서버(6.5절) | claude-api, mcp-builder | 두 제공자로 같은 후보 검토, 캐시 적중 확인, MCP 평가 질문 10개 통과 |
| 6+. 이후 확장 | 탐지 파라미터 UI·규칙 추가 → LIVE 다채널·LIVE→VOD 연결 → 다른 플랫폼(SOOP·유튜브) | `add-detection-rule`, `add-platform` | 단계별 별도 계획 |

## 13. 기능 동등 체크리스트 (legacy 기준)

- [x] VOD 채팅 페이지 수집·DB/TXT 저장·중단 위치 이어받기 (2단계, 실제 다시보기로 확인)
- [x] 샘플로 먼저 체험(데모) — 저장된 세션으로 검토·내보내기 (2단계)
- [x] 공개 LIVE 채팅 수집·전체 대화 보관·최근 300건 표시 (3단계, 실제 생방송으로 확인)
- [x] 자동 후보 D1~D5 판정, 임시 후보 표시 (1단계, 기존 결과와 필드 단위 일치)
- [x] 소규모 채널 민감도 — 수집·다시 분석 화면에서 선택 (2단계)
- [x] 영상 댓글 타임스탬프 추천(수집·직접 붙여넣기) (4단계, 시각 파서는 기존과 46개 사례 필드 단위 일치, 실제 다시보기 댓글 확인 대기)
- [x] VOD 후보 클릭 → 재생 이동·재생 시각 채팅 따라가기 (2단계, 실제 다시보기로 확인)
- [x] LIVE 후보 앞뒤 30초 채팅 문맥(최대 300건) (3단계, 화면은 VOD와 같은 편집 구간 채팅, `/context` API 제공)
- [x] LIVE 자동 재연결·공백 표시·중단·같은 방송 이어 기록 (3단계)
- [x] 내보내기: 전체 채팅 TXT/JSONL, 후보 TXT/CSV/JSON, 후보별 주변 채팅 (2단계)
- [x] 내보내기: LIVE checkpoint (3단계)
- [x] 웹 시청자 수 기록·10% 감소 시 기준 재학습 (3단계, 기존 LIVE 분석기와 후보 필드 단위 일치)
- [x] 저장 목록 숨김·복원 (4단계)
- [x] 선택형 AI(미리보기 확인 후 실행) (4단계, OpenAI 응답 형식은 가짜 서버로 확인, 실제 키로 확인 대기)
- [x] 127.0.0.1 바인딩·로컬 접근 키·Host·Origin 검사, 보안 헤더·CSP (0단계)
- [x] 작성자 가명화 (2~3단계, 세션별 HMAC. LIVE 재전송 판별 키도 HMAC으로만 저장)

보류 (기능 동등 범위에서 제외, 필요해지면 별도 계획):

- 본인 시청 계정 로그인 VOD (기존 Playwright 의존)
- 공식 API 시청자 수 기록 (개발자 Client ID/Secret 사용)

두 기능 모두 원본에서도 실제 동작 검증이 남아 있던 기능입니다. 공개 웹 시청자 수 기록·보정은 그대로 포함합니다.

## 14. 위험 요소

| 위험 | 대응 |
|---|---|
| 치지직 비공식 엔드포인트 변경 | 어댑터에 응답 형식 검증 집중, `ErrSchemaChanged`로 명시적 실패 |
| 탐지 결과가 기존과 미세하게 다름 (중앙값·반올림) | 1단계에서 데모 데이터로 Python 결과와 나란히 비교 |
| 로그인 VOD를 보류해 로그인이 필요한 VOD는 수집할 수 없음 | 수집 시 원인을 안내하는 오류 표시. 재개 시 `chromedp` 등 Go 대안 검토 |
| 프론트 빌드 도구 추가로 개발 환경 복잡도 증가 | 배포물에는 빌드 결과만 내장 → 사용자는 Node 불필요 |
| Claude API 형식 변경 (모델 세대마다 사고·출력 설정이 바뀜) | 모델 목록 하드코딩 금지, `internal/ai` 수정 시 claude-api 스킬로 최신 형식 확인 |
| AI 비용 증가 (자동 분석) | 토큰 카운트 미리보기, 비용 한도, 배치 API, 프롬프트 캐싱 |
| MCP로 채팅 본문이 Claude에 전달됨 | 사용자가 직접 등록할 때만 동작, README·PRIVACY에 전송 범위 명시, 원문 응답 미제공 |
| E2E가 Python에 의존 | 개발 환경 전용, 배포물·사용자 설치에는 영향 없음 |

## 15. 결정 기록

| 날짜 | 질문 | 결정 |
|---|---|---|
| 2026-10-16 | 로그인 VOD·공식 API 시청자 수를 기능 동등 범위에 포함할지 | 보류 (13절) |
| 2026-10-16 | 앱 이름 | MomentForge로 변경 |
| 2026-10-16 | Go 최소 버전 | 1.23 이상 → 0단계에서 **1.26 이상**으로 상향 (`modernc.org/sqlite` v1.60 요구). 설치된 버전 1.27.2 |
| 2026-10-16 | AI 기본 제공자 | 5단계부터 Claude 기본, OpenAI 선택 |
| 2026-10-16 | MCP 서버 포함 시점 | 5단계 |
| 2026-10-16 | 스킬 적용 방식 | 외부 스킬 4개를 저장소에 포함(커밋 `dbd4588` 고정), 규칙은 프로젝트 스킬로 작성, Go 파일 수정 시 훅으로 자동 검사 (2절) |
| 2026-10-16 | 공용 타입 위치 | `internal/domain` 신설. 작성자 가명화는 어댑터가 아닌 `app`에서 수행 (6.1절) |
| 2026-10-16 | 데이터 폴더 기본 위치 (0단계) | 실행한 현재 폴더의 `.momentforge/`. 시작할 때 절대 경로를 출력하고 `--data`로 바꿀 수 있음 |
| 2026-10-16 | 서버 중복 실행 방지 (0단계) | `runtime/server.lock`에 SQLite 배타 잠금. 추가 의존성 없이 모든 OS에서 동작하고, 프로세스가 죽으면 OS가 해제 |
| 2026-10-16 | SSE 연결 방식 (0단계) | `EventSource` 대신 fetch 스트리밍. 접근 키를 URL이 아닌 헤더로 보내기 위함. 끊기면 1초부터 최대 10초 간격으로 재연결, `overflow` 이벤트를 받으면 모든 데이터를 다시 불러옴 |
| 2026-10-16 | API 테스트 구조 (0단계) | `api`는 `app.App` 대신 필요한 메서드만 담은 `api.Core` 인터페이스를 받음. 실제 조립은 `cmd/momentforge` 통합 테스트로 확인 |
| 2026-10-16 | `go ./...`에서 `web/node_modules` 제외 (0단계) | `go.mod`의 `ignore ./web/node_modules` (Go 1.25 이상 기능) |
| 2026-10-16 | 화면 서체 (0단계) | 운영체제 한글 서체 사용, 웹폰트 미포함 (사용자 확정, DESIGN-SYSTEM.md 3절) |
| 2026-10-16 | 화면 색 | 사용자가 제공한 참고 이미지 기준의 흑백 테마. 라이트 = 흰 상의·은발, 다크 = 검은 가디건, 강조 = 눈동자 파랑·볼 홍조 (DESIGN-SYSTEM.md 2절) |
| 2026-10-16 | 한글 정규화 (1단계) | NFC는 한글 자모 조합만 직접 구현. 외부 의존성(`golang.org/x/text`)을 피하기 위함이며, 탐지 규칙에 영향을 주는 범위는 한글뿐 |
| 2026-10-16 | 규칙 키 이름 (1단계) | D1~D5 → `laugh`, `surge`, `repeat:<문장>`, `emotion:<종류>`, `participants`. 화면의 이유 이름은 그대로 |
| 2026-10-16 | 기존 비교 고정 (1단계) | `detect.CzzEditParams()`와 `rules.CzzEdit()`를 고정값으로 두고 비교 테스트가 이것을 씀. 새 규칙·기본값 조정은 `rules.All()`·`DefaultParams()`에서 |
| 2026-10-16 | 근거 채팅 미리보기 (1단계) | 판정 근거는 기존과 같게 두고, 화면 미리보기만 같은 문장끼리 묶어 많이 나온 순서로 표시 (급증 신호의 근거에 평소 대화가 섞여 반응 문장이 가려지던 문제) |
| 2026-10-16 | 데모 저장 (1단계) | 1단계 데모는 요청마다 분석만 하고 저장하지 않음. 세션 저장·내보내기가 생기는 2단계에서 기존처럼 저장 |
| 2026-10-16 | 3절 성능 진단 정정 (1단계) | 기존 코드도 최근 256개 창 통계를 캐시하고 있어 "평가마다 90개 구간 재계산"은 정확하지 않았음. 실측 비교로 대체 |
| 2026-10-16 | API와 저장소 분리 (2단계) | `api`는 `app`의 화면용 타입(`SessionInfo`, `PlaybackInfo`, `AnalysisView`)만 사용. 구조 검사 훅이 `api`의 `store`·`platform` import를 잡아낸 뒤 결정 |
| 2026-10-16 | 파일 다운로드 (2단계) | 기존의 `?key=` URL 대신 fetch로 헤더에 접근 키를 실어 받고 브라우저에 저장 |
| 2026-10-16 | 진행률 전달 (2단계) | 폴링 대신 SSE `job` 이벤트. 작업은 한 번에 하나(`BUSY`), 앱 종료 시 수집을 안전한 지점에서 일시정지 |
| 2026-10-16 | 로그인 필요 VOD (2단계) | 401 응답에 "로그인이 필요한 다시보기는 아직 지원하지 않습니다" 안내 (로그인 수집 보류) |
| 2026-10-16 | 플랫폼 주소 변경 수단 (2단계) | 어댑터 주소는 테스트용 옵션 함수로만 바꿀 수 있음. 실행파일에 환경변수 등 외부 설정 경로를 두지 않음 |
| 2026-10-16 | 영상 재생 라이브러리 (2단계) | `hls.js/light`를 필요할 때만 불러옴(gzip 114KB). Safari는 내장 HLS 사용 |
| 2026-10-16 | 샘플 세션 (2단계) | 저장된 샘플이 있으면 새로 만들지 않고 그 세션을 염 |
| 2026-10-16 | 재생 위치 채팅 조회 (2단계) | 재생 위치를 10초 단위로 묶어 조회(앞뒤 30초, 앞뒤 150건). 재생이 이어질 때 요청이 10초에 한 번 이하 |
| 2026-10-16 | 2단계 실제 확인 | 사용자가 준 치지직 다시보기(4시간 32분)로 확인. 42페이지·채팅 8,257건 수집(격리·공백·작성자 누락 없음), 후보 16개. 같은 채팅을 CzzEdit 탐지기에 넣은 결과와 후보 16개·창 3,266개 모든 필드 일치. 앱 내장 브라우저에서 후보 클릭 → 편집 구간 시작으로 영상 이동·재생, 재생 위치 채팅 동기화 확인 |
| 2026-10-17 | LIVE 동시 수집 수 (3단계) | 기존처럼 한 번에 하나(`LIVE_BUSY`). VOD 작업과는 별개로 동시에 실행됨. 6.4절의 다채널 동시 수집은 6+단계로 미룸 |
| 2026-10-17 | LIVE 저장 구조 (3단계) | `chat_events`를 VOD와 함께 쓰고 LIVE 전용 열(받은 시각, 연결 번호, 늦음, 시간 품질, 재전송 키)을 추가(마이그레이션 0003). 상태는 `live_state`, 후보는 revision을 가진 `live_candidates` |
| 2026-10-17 | 재접속 때 다시 오는 채팅 (3단계) | 연결 직후 받은 과거 채팅을 HMAC 재전송 키로 이전 연결분과 비교해 같은 횟수만큼 버림. 플랫폼 메시지 ID 원문은 저장하지 않음 |
| 2026-10-17 | LIVE 시간 기준 (3단계) | 방송 시작 시각을 받으면 `broadcast_elapsed`, 못 받으면 수집 시작 기준 `capture_elapsed`. 수집 시작 전 구간은 `unobserved`, 끊긴 구간은 `disconnected` 또는 오류 코드로 공백 기록 |
| 2026-10-17 | 앱 재시작 후 LIVE (3단계) | 실행 중 종료된 기록은 시작할 때 `incomplete`(`PROCESS_RESTART`)로 바꾸고 진행 중 후보는 부분 문맥으로 마감. 같은 방송이면 이어 기록 가능 |
| 2026-10-17 | LIVE 반응 타임라인 (3단계, 기존에 없던 화면) | VOD와 같은 밀도 타임라인을 LIVE에도 둠. 구간별 채팅 수를 DB에서 세어(5초 이상, 약 1,800칸) 분석 완료 시점이 10초 진행할 때마다 갱신, 미관측 구간은 빗금 |
| 2026-10-17 | LIVE 채팅 목록 (3단계) | 9절의 가상 스크롤 대신 최근 500건만 메모리에 두고 표시. 위로 스크롤하면 목록을 멈추고 새 채팅 수를 알림 |
| 2026-10-17 | 공백 기록 형식 (3단계) | 내보내기 manifest의 `issues`를 CzzEdit와 같은 `{start_ms, end_ms}`로 씀. 2단계 VOD manifest가 Go 기본 필드명(`StartMs`)으로 쓰던 문제도 함께 고침 |
| 2026-10-17 | LIVE E2E (3단계) | 빌드 태그 `e2efake`의 가짜 생방송 플랫폼(`https://live.e2e.test/채널`)을 E2E 전용 실행파일 `bin/momentforge-e2e`에만 넣음. 배포용 `bin/momentforge`에는 포함되지 않음 |
| 2026-10-17 | 판정 시작 조건 (사용자 요청, 6.2.1) | 고정 15건·5명 대신 "최근 2시간 평소 채팅 수의 3.75배" 또는 "최근 15분 평소 작성자 수의 5배" 중 하나를 넘으면 판정. 둘 다 상한 없음, 최소 6건·3명, 처음 3분은 판정하지 않음. 규칙 2.0.0, 민감도 옵션 이름은 "항상 가장 민감하게" |
| 2026-10-17 | 버린 기준 (6.2.1 실험) | 전체 기간 상위 3%·최대 15건: 큰 방 폭증과 새벽 대응 불가. 최근 15분만: 뜨거운 구간을 평소로 학습. 상위 분위수: 반응이 잦으면 반응 자체가 기준이 됨. 시청자 수 직접 사용: 다시보기에 자료가 없고 채널별 채팅 습관 차이가 더 큼 |
| 2026-10-17 | 3단계 실제 확인 | 사용자가 준 치지직 생방송(시청자 74~83명)을 수정 전 빌드로 확인. 32분 연속 수집(연결 1회 유지, 오류·늦은 채팅·중복 0, 채팅 625건, 분석 완료 시점이 실제 시간만큼 진행, 시청자 표본 1분마다), 현재까지 저장(받은 순서·시간순 TXT, JSONL 633건 일치, 미관측 구간 CzzEdit 형식), 중단 → 이어 기록(연결 복구 1회, 중단 구간이 두 번째 공백), 다시 중단까지 정상. 발견해 고친 것: 중단 직후 내보내기 목록이 최종 저장 전에 갱신되던 경합, 저장 실패 시 채팅이 로그만 남기고 사라질 수 있던 경로. 이 방송은 고정 기준에서 후보 0개였고, 이것이 6.2.1 판정 시작 조건 개선의 계기 |
| 2026-10-17 | 영상 댓글 저장 (4단계) | 기존의 JSON 기록 대신 `comment_captures`·`comments`·`viewer_recommendations` 테이블(마이그레이션 0004). 작성자는 세션 HMAC 가명, 삭제·비밀·클린봇 댓글은 본문을 비워 저장. 시각 파서는 legacy 파서를 실제로 돌린 46개 사례(`internal/app/testdata/comment_parse.json`)와 필드 단위 일치 |
| 2026-10-17 | 댓글만 수집한 작업 (4단계) | 상태 `comments_only`. 화면의 "채팅 수집"은 같은 작업에 채팅을 이어 받아 댓글과 채팅이 한 작업에 모임. 다시보기 수집은 기본으로 댓글도 읽고, 댓글 실패는 채팅을 지키고 경고만 표시(기존과 같음) |
| 2026-10-17 | 통합 타임라인 (4단계) | 기존처럼 타임라인 기록을 매번 새로 저장하지 않고, 최신 분석·댓글 수집·AI 실행으로 바로 계산. 출처가 바뀔 때마다 `exports/<작업>/timeline/`의 TXT·CSV·JSON을 다시 씀 |
| 2026-10-17 | 선택형 AI (4단계) | 기존과 같은 OpenAI Responses API를 표준 라이브러리로 호출(의존성 추가 없음). 제공자 공통 인터페이스(`internal/ai`) 뒤에 두어 5단계 Claude를 같은 흐름에 붙임. 모델 ID는 기존처럼 사용자가 입력(목록 조회는 5단계). 추정치는 기존과 같은 UTF-8 바이트 기반. 실행 기록은 `ai_runs`(마이그레이션 0005), 앱이 실행 중 꺼진 실행은 결과 불명으로 "일부" 처리하고 자동 재전송하지 않음 |
| 2026-10-17 | 숨김·복원 (4단계) | 기존처럼 전체 숨김·전체 다시 표시. 숨긴 뒤 "샘플로 먼저 체험"은 저장된 샘플을 다시 표시(중복 생성 안 함) |
| 2026-10-17 | 4단계 E2E | E2E 전용 가짜 플랫폼에 가짜 다시보기·댓글(`https://live.e2e.test/video/번호`)과 가짜 AI 제공자를 더해, 수집→댓글→통합 타임라인→AI 미리보기·실행을 외부 연결 없이 확인 |
| 2026-10-17 | 테스트의 외부 연결 방지 (4단계) | 서버 통합 테스트가 다시보기 수집 뒤 댓글 단계에서 실제 네이버 댓글 API에 영상 번호 1234로 공개 조회를 한 번 보낸 것을 발견. 테스트용 `WithAPIBase`가 댓글 주소도 가짜 서버로 돌리도록 바꿔 재발을 막음 |
| 2026-10-17 | 4단계 실제 확인 (영상 댓글) | 사용자가 지정한 2단계 다시보기(15554542)를 채팅부터 다시 받고 댓글까지 읽음. 채팅 수집·분석 정상(새 판정 기준으로 후보 24개, 6.2.1 실험값과 같음), 공개 댓글 1페이지·4개(플랫폼 보고 수와 같음, 누락 없음), 작성자는 모두 가명, 내보낸 댓글 파일에 닉네임·원본 ID 없음, 화면 정상. 이 영상 댓글에는 시각이 없어 시청자 추천 0개이며 기존 파서도 같은 결과. **실제 댓글의 시각 추출은 시각이 적힌 다시보기로 다시 확인해야 함**. 확인 중 수집 작업의 페이지 수가 댓글 페이지 수로 덮이던 표시 문제를 고침 |
| 2026-10-17 | Claude 제공자 (5단계) | 공식 Go SDK, 기본 모델 `claude-opus-5-5`, effort `high` 명시, 사고는 모델 기본(적응형), 구조화 출력, 고정 지시문에 `cache_control`, 출력 상한 8,000(사고 포함). SDK의 환경변수 기본값(`ANTHROPIC_BASE_URL`·프로필)과 자동 재시도를 끔. 화면 기본 제공자는 Claude, OpenAI는 선택 |
| 2026-10-17 | AI 지시문 2판 (5단계) | CzzEdit 지시문 앞부분은 그대로 두고 입력 형식·판단 기준·출력 규칙을 더함. Claude 캐시 최소 길이(512토큰)를 넘기기 위한 것이기도 하며, 두 제공자가 같은 지시문을 씀(비교 공정성). 같은 계획이라도 1판과 지문(fingerprint)이 달라짐 |
| 2026-10-17 | 정확한 토큰 계산 시점 (5단계) | 설계(6.3)는 "실행 전 토큰 카운트 API"였으나, 카운트 요청에도 채팅이 전송되므로 사용자가 미리보기를 본 뒤 "정확한 토큰 수 계산"을 누를 때만 보냄. 미리보기의 최대 비용은 전송 없이 바이트 기반 상한으로 항상 계산 |
| 2026-10-17 | 자동 분석의 범위 (5단계) | "모든 후보 자동 선택" + 비용 한도 필수 + 미리보기 확인 후 실행. 수집이 끝나면 확인 없이 저절로 보내는 방식은 "AI 전송은 사용자가 미리보기를 확인한 뒤에만" 원칙(CLAUDE.md)과 맞지 않아 구현하지 않음 |
| 2026-10-17 | 배치 (5단계) | Message Batches. 요청 ID는 `chunk-NNN`(설계의 "후보 ID" 대신: 요청 하나가 시간 창이고 후보 여러 개를 담거나 겹치는 창에 같은 후보가 들어갈 수 있음). 요청 한도는 바로 보내기 20개, 배치 100개(비용 한도가 대신 제한). 앱을 꺼도 배치는 계속, 재시작 후 키가 있으면 결과를 가져오고 다시 보내지 않음 |
| 2026-10-17 | 가격표 (5단계) | `internal/ai/anthropic`에 2026-10 공시 가격(Opus 5.5 $4/$20, Sonnet 5.5 $2/$10, Haiku 5.5 $0.10/$0.50, Fable 5.1 $10/$50, 캐시 쓰기 1.25배, 배치 50%)을 두고 화면에 기준 시점을 표시. OpenAI 가격은 확인된 자료가 없어 두지 않음(토큰만 표시, 비용 한도 불가) |
| 2026-10-17 | 거절 시 대체 모델 (5단계) | claude-api 스킬은 서버 측 대체 모델(fallbacks)을 기본으로 켜라고 권하지만, 사용자가 미리보기에서 확인하지 않은 모델로 채팅이 전송되므로 켜지 않음. 거절은 `AI_REFUSAL`로 표시하고 다른 구간·모델을 안내 |
| 2026-10-17 | MCP 서버 (5단계) | `momentforge mcp --data`, stdio, SQLite 읽기 전용 연결. 테스트에서 저장소를 직접 만들 수 없는 구조 규칙(`internal/mcp` → `app`, `domain`만) 때문에 MCP 통합 테스트와 평가 확인은 `cmd/momentforge`에 둠 |
| 2026-10-17 | MCP 평가 실행 방법 (5단계) | 공식 평가 스크립트(`mcp-builder/scripts/evaluation.py`)는 Anthropic API 키가 필요하고 이 환경의 `claude` CLI는 실행되지 않아, 정답을 모르는 별도 에이전트가 MCP 도구만 쓰는 stdio 도우미로 질문 10개를 풀게 함: 1차 10/10. 1차 의견(작성자 필터 없음, 최대 개수 안내 없음, 근거 수·겹침·AI 실행 ID 없음, 창이 영상 끝을 넘음, 구간 경계 설명 없음)을 반영한 뒤 2차도 10/10(작성자 필터·겹침 정보로 호출 수 감소). 2차 의견 중 Markdown 후보 목록에 근거 수·겹침·배율을 더함 |
| 2026-10-17 | 5단계 E2E | 가짜 AI 제공자 두 개(가격·토큰 계산·모델 목록·배치를 갖춘 것, 바로 보내기만 하는 것)로 비용 미리보기 → 정확한 토큰 계산 → 비용 한도 배치 → 다른 제공자로 같은 후보 → 비교까지 외부 연결 없이 확인. E2E 서버는 API 키 환경변수를 지우고 실행하고, 배치 확인 간격은 E2E 실행파일에서만 0.2초 |
| 2026-10-17 | OpenCode Go 제공자 (사용자 요청) | Go 공식 문서는 Go를 "OpenCode와 다른 코딩 에이전트용"으로 두고, 직접 호출하는 프로그램에 코딩 에이전트 트래픽·자체 User-Agent·세션 헤더를 요구하며 남용을 감시함. 사용자 제안대로 API를 직접 부르지 않고 OpenCode CLI를 하네스로 실행(6.3). Go가 제공하는 모델을 형식(채팅 완성·Messages·Responses) 구분 없이 모두 씀. 채팅이 비코딩 요청이라는 점은 그대로이므로, 구독 제한 위험은 사용자가 알고 선택함 |
| 2026-10-17 | OpenCode 하네스 확인 (실제 실행) | 설치된 OpenCode v2.0.32로 작은 요청(수집 채팅 없음)을 보내 확인: 표준 입력을 닫지 않으면 무한 대기, 표준 입력이 메시지로 쓰임(세션 내보내기로 확인), 오류 이벤트 형식, `session delete`로 정리됨. 세션 DB를 따로 두면(`OPENCODE_DB`) 로그인 정보도 빠져 쓸 수 없음. 사용자 계정에 활성 Go 구독이 없어 403("An active OpenCode Go subscription is required")이었고, 무료 모델은 `opencode run`에서 "OpenCode 안에서만"이라며 거절해 **성공 응답 형식과 도구 차단은 실제로 확인하지 못함**. 출력 해석은 v1 공개 형식과 v2 오류 형식을 함께 받도록 만들고 가짜 CLI로 시험 |
| 2026-10-17 | OpenCode 기록과 전송 범위 | OpenCode 개인 서버가 사용자의 전역 설정(AGENTS.md·스킬·플러그인)을 읽으므로 그 내용도 지시문에 함께 들어갈 수 있음. 채팅은 OpenCode를 거쳐 모델 제공자로 전송되고(모델별 보관: 대부분 0일, 일부 30일, Gemini Argon·Muse Spark는 학습 사용), 실행 뒤 OpenCode 세션은 지움 |
| 2026-10-17 | CzzEdit 0.3.2 남은 파일 정리 (사용자 확인) | 13절 기능 동등 항목이 모두 끝나 루트의 `czzedit/`, `ARCHITECTURE.md`, `RELEASE_NOTES.md`, `read.md`, `Image/`, `pyproject.toml`, `requirements.txt`, `requirements-login.txt`, 설치·실행 스크립트(`install.sh`·`.ps1`·`.cmd`, `run.sh`·`.cmd`, `설치.command`, `실행.command`)를 삭제. 모두 `legacy-python` 브랜치에 같은 내용으로 남아 있고, 기존 결과 비교 스크립트는 `git archive legacy-python czzedit`로 브랜치에서 읽음. `LICENSE`(MIT)와 `.gitignore`(`.czzedit/` 제외 포함)는 유지. README·PRIVACY는 MomentForge 기준으로 새로 쓰고 외부 전송 범위(플랫폼, 제공자별 AI 요청, 정확한 토큰 계산, OpenCode CLI, MCP)를 명시해 14절 MCP 위험 대응을 마침. `read.md`의 TXT 수동 활용법은 옮기지 않음: MCP 서버와 앱 안 AI가 대신하고, 내보내기 파일 구성은 README에 둠 |
| 2026-10-17 | 화면 구조: 편집 작업대 (사용자 선택) | 사용자가 "아래로 내린 구조"를 지적해 세 안 중 "편집 작업대"를 고름. 왼쪽에 작업 목록 사이드바를 늘 두고(넓은 화면에서 접기, 휴대폰은 서랍), 홈은 '새 작업' 화면(다시보기·생방송 탭, 다른 종류 주소를 붙여넣으면 탭 전환)으로 바꿈. 작업 화면은 창 높이에 맞춰 영상·재생 위치 채팅·타임라인을 고정하고 오른쪽을 후보·AI 검토·댓글·파일 탭으로 나눔(생방송은 후보·수집 상태·파일). 스크롤 뒤 내려오던 제목 막대(PageBar)와 "처음으로" 링크는 사이드바가 대신해 없앰. 타임라인에 재생 위치 선과 위치 이동(누르기·방향키)을 더하고, 영상이 없는 작업은 그 위치의 채팅을 보여 줌. 생방송 입력 칸을 `features/home`으로 옮겨 기능 폴더끼리의 import를 없앰 |
| 2026-10-17 | Tracwell 테마 적용 (사용자 요청) | 사용자가 Tracwell 구조를 충분히 적용했는지 물었고, 앱 견본(사이드바·요약 숫자·막대 목록·카드 셸)과 랜딩 구성을 비교해 보고한 뒤 "해당 페이지 테마와 카드나 디자인들을 최대한 적용"을 요청함. 사이트 CSS의 중립 테마·데이터 색·모서리·그림자·버튼 모양을 토큰으로 옮기고, 참고 이미지 색(2026-10-16)과 "카드 대신 구분선" 원칙을 교체. 카드 셸·시트·떠 있는 틀, 요약 숫자 카드, 근거 채팅 비율 막대, 알약 탭, 세 단계 카드, 떠 있는 휴대폰 머리글을 더함. 주요 버튼 파랑은 흰 글자 대비 4.5:1을 위해 조금 진하게 함. Inter 서체 파일은 넣지 않고 운영체제 UI 서체를 씀(실행 중 외부 서체 금지·실행파일 크기). 자세한 값은 `docs/DESIGN-SYSTEM.md` 2·3·4·7절 |
| 2026-10-17 | 앱 안 영상 재생 순서 (사용성 점검) | Chrome 141 이후는 HLS 자체 재생을 지원한다고 답하는데, 그 경로는 먼 위치로 이동할 때 영상을 처음부터 내려받아 4시간 다시보기의 2:40:00 후보가 45초 넘게 재생되지 않았음. hls.js를 먼저 쓰고, Media Source를 지원하지 않는 환경(iPhone Safari)에서만 브라우저 자체 재생을 씀. 또 후보의 "영상에서 보기"를 누르면 영상이 화면 밖일 때 영상 쪽으로 스크롤함(움직임 줄이기 설정이면 즉시) |
| 2026-10-17 | 화면 움직임 (사용자 요청) | 참고 사이트 두 곳(Lucian UX/UI Prompt, Tracwell)의 코드를 분석해 작업 도구에 맞는 움직임만 적용(사용자 선택: "작업 도구에 맞게"). 원칙 3을 "상태 변화에 대한 응답"으로 넓히고 홈 제목 등장 하나만 예외로 둠. 패턴·토큰·규칙은 DESIGN-SYSTEM.md 8절. 후보 출처 선택 상자를 선택 알약(라디오 묶음)으로 바꿈. E2E는 움직임 줄이기 환경에서 돌리고 `test_motion.py`가 움직임을 켜고 확인 |
| 2026-10-17 | 생방송 영상 재생 (사용자 요청, 기존에 없던 기능) | 다시보기용 `PlaybackSource`와 따로 선택 인터페이스 `LivePlaybackSource`를 둠(생방송만·다시보기만 재생하는 플랫폼을 구분하고, E2E 가짜 다시보기 화면을 바꾸지 않기 위함). 같은 `GET /api/sessions/{id}/playback`이 작업 종류에 따라 고름. 치지직은 `LiveMeta`가 읽는 같은 `live-detail`(v2)의 `livePlaybackJson`에서 `mediaId` HLS(없으면 LLHLS)를 쓰고, 다시보기와 같은 `paidLive`·`playbackAuthType` 검사에 방송 중·연령 제한·유료 상품 검사를 더함. 화면 요청이라 재시도 없이 한 번(8초) 요청. 실제 확인에서 생방송 CDN이 요청마다 바뀌는 것(GCDN `ex-nlive-streaming.navercdn.com`, LCDN `livecloud.pstatic.net`, NVELOP `nvelop-livecloud.pstatic.net`)을 발견해 생방송에만 `*.pstatic.net`을 허용하고 치지직 CSP 미디어 출처에 더함(다시보기는 `*.navercdn.com`만). 재생 주소는 기존처럼 돌려주기만 하고 저장하지 않음. 화면은 생방송 무대에 '영상' 카드를 두되 수집 중일 때만 재생기를 띄움(멈춘 기록에서 열면 그때가 아니라 지금 방송이 나오고 외부 요청도 생기기 때문). hls.js는 재생을 누를 때 받기 시작하고 멈추면 받기를 멈춤(`autoStartLoad: false`, 몇 시간 띄워 두는 작업이라). 치지직 생방송 재생 목록은 10초 조각이라 선언하고 2초 조각을 담으므로, 기본 지연(목표 길이 3배 = 30초 창의 맨 앞) 대신 초 단위 지연을 씀(실제 약 11초). 그 재생 목록의 `EXT-X-DATERANGE` 때문에 hls.js가 `data:` 트랙을 만들어 CSP에 막히므로 시간 메타데이터 큐를 끔(다시보기도 같이). 무대가 좁아 영상 위·채팅 아래로 나뉘면 생방송은 채팅에 더 많은 높이를 줌. E2E는 가짜 플랫폼이 루프백 서버에서 만드는 무음 AAC 생방송 HLS를 실제로 재생해 확인(외부 연결 없음) |
| 2026-10-17 | 생방송 기록의 AI 검토 (사용자 선택) | 그동안 다시보기 작업대에만 있던 AI 검토를 생방송 작업대에도 둠. 사용자 선택: **수집 중에도** 열고, 보낼 범위는 **다시보기와 같은 세 방식**(고른 후보, 확정 후보 전체 자동 선택 + 비용 한도 필수, 채팅 구간 직접 지정). 자동 전송은 없고 매번 미리보기 → 확인으로만 보냄. 계획은 미리보기를 만든 순간 저장된 채팅(최대 seq)으로 고정해 그 뒤 들어온 채팅은 넣지 않음(계획에 채팅 본문이 들어 있어 보내는 내용은 미리보기와 같음). 후보는 `confirmed`·`partial`만 고를 수 있음(`generating`·`waiting_context`는 편집 구간이 아직 늘어나서 제외). 구간은 분석이 끝난 위치까지만 지정. 늦게 받은 채팅도 그 시각의 채팅이라 함께 보냄. AI 추천은 통합 타임라인(LIVE는 자동 탐지 후보 + AI 추천)으로 받아 후보 탭(출처 선택)과 반응 타임라인 마커에 표시. 제공자·키·비용 한도·배치·결과 비교는 다시보기와 같고, 화면 코드는 `web/src/shared/ai/`로 옮겨 두 작업대가 함께 씀 |

새로 결정이 필요한 사항이 생기면 이 표에 추가합니다.

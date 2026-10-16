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
│  │  └─ openai/                  기존 기능 동등용
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
- 모든 임계값은 `detect.Params`. `DefaultParams()`는 새 분석의 기본값이고, `CzzEditParams()`와 `rules.CzzEdit()`는 기존 결과 비교용으로 고정합니다.
- 같은 엔진이 VOD(한 번에 분석)와 LIVE(`Add`, `Gap`, `Evaluate`, `Prune`로 증분 분석)를 처리합니다. 과거 10초 칸 통계는 한 번 계산해 재사용하고, 늦게 도착한 채팅이 들어오면 해당 칸만 다시 계산합니다.
- **기능 동등 확인**: `internal/detect/rules/testdata/legacy/`의 4가지 경우(데모, 데모+민감도, 공백·명령·작성자 누락·자모 분리·180초 분할이 섞인 합성 데이터 2종)에서 후보와 5초 창의 모든 필드가 CzzEdit 0.3.2와 같습니다 (`TestMatchesLegacy`). 비교 데이터는 `gen_cases.py`로 다시 만들 수 있습니다.
- **속도** (5시간, 채팅 52,114건, 같은 입력): CzzEdit 630ms → Go 109ms. 결과는 같습니다.
- 시청자 수 보정(기준선 시작 시점 이동, 기준 전환 시 후보 마감)은 3단계에서 `Evaluate(t, baselineFrom)`과 `Grouper`에 연결합니다.

### 6.3 AI 제공자 (확장 1순위, claude-api 스킬 적용)

```go
type Provider interface {
    ID() string                                            // "anthropic", "openai"
    Models(ctx context.Context) ([]Model, error)           // Models API로 실시간 조회
    Estimate(ctx context.Context, req ReviewRequest) (Cost, error)   // 토큰 수·예상 비용
    Review(ctx context.Context, req ReviewRequest) (ReviewResult, error)
}

// 선택 기능: 지원하는 제공자만 구현
type BatchProvider interface {
    SubmitBatch(ctx context.Context, reqs []ReviewRequest) (BatchID, error)
    BatchResults(ctx context.Context, id BatchID) (iter.Seq2[ReviewResult, error], error)
}
```

Claude 구현 규칙 (claude-api 스킬 기준):

| 항목 | 규칙 |
|---|---|
| SDK | `github.com/anthropics/anthropic-sdk-go`. 표준 라이브러리로 직접 HTTP를 호출하지 않음 |
| 기본 모델 | `claude-opus-5-5`. 대량 1차 분류는 `claude-haiku-5-5` 선택 가능. 모델 목록은 하드코딩하지 않고 Models API로 조회 |
| 사고·effort | 적응형 사고(기본값). Opus 5.5의 effort 기본값이 `medium`이므로 `OutputConfig.Effort`를 **명시적으로 설정** (후보 검토는 `high`, 대량 분류는 `low`). `budget_tokens`·사고 비활성화는 사용하지 않음 (400 오류) |
| 출력 형식 | 구조화 출력 `output_config.format`(JSON 스키마)으로 받음. 어시스턴트 응답 미리 채우기(prefill)와 `tool_choice` 강제는 최신 모델에서 400 오류이므로 사용하지 않음 |
| 프롬프트 캐싱 | 순서를 **고정 시스템 프롬프트(판정 기준·출력 규칙) → 세션 공통 자료 → 후보별 채팅**으로 두고 앞쪽에 `cache_control`. 시스템 프롬프트에 현재 시각·무작위 ID를 넣지 않음. `usage.cache_read_input_tokens`로 캐시 적중 확인 |
| 긴 요청 | 입력·출력이 길면 스트리밍 사용 (요청 시간 초과 방지) |
| 미리보기 | 실행 전 토큰 카운트 API로 입력 토큰과 예상 비용을 미리보기 화면에 표시 |
| 자동 분석 | VOD 전체 후보처럼 급하지 않은 작업은 **배치 API(비용 50%)**. 결과는 `custom_id`(후보 ID) 기준으로 매칭 (순서 보장 없음) |
| 오류 처리 | `errors.As`로 `*anthropic.Error`를 꺼내 상태 코드별 분기: 429·5xx·연결 오류는 재시도, 400·404는 즉시 실패 후 사용자에게 원인 표시 |
| 인증 | API 키는 서버 메모리에만 보관 (기존 원칙). 환경변수 `ANTHROPIC_API_KEY`가 있으면 사용. 설정 파일에 저장하지 않음 |

공통 흐름은 유지: **미리보기(전송 내용·예상 비용) → 사용자 확인 → 실행**. 자동 분석은 사용자가 켜고 비용 한도를 정한 경우에만 동작합니다.

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
| `ai_runs` | id, session_id, provider, model, mode(single/batch), state, request_json, result_json, input_tokens, cache_read_tokens, output_tokens | 비용·캐시 적중 기록 |

- WAL 모드, 쓰기는 작업별 단일 writer goroutine, 읽기는 커넥션 풀. MCP 서버는 읽기 전용 연결로 같은 DB를 엶.
- 마이그레이션은 `store/migrations/NNNN_*.sql` + `PRAGMA user_version`.
- DB가 원본 기준이고 TXT는 DB에서 재생성 가능하다는 원칙 유지.
- 데이터 폴더: 실행한 위치의 `.momentforge/` (`--data`로 변경). 하위에 `data/`(DB·백업), `exports/`, `runtime/`(접근 키, 서버 락).

## 8. HTTP API

| 메서드·경로 | 용도 |
|---|---|
| `GET /api/status` | 버전·플랫폼 기능 목록 |
| `POST /api/resolve` | URL → 플랫폼·대상·지원 기능 |
| `GET/POST /api/sessions`, `GET/PATCH /api/sessions/{id}` | 세션 목록·생성·숨김/복원 |
| `POST /api/sessions/{id}/capture`, `/stop`, `/resume` | VOD·LIVE 수집 제어 |
| `GET /api/sessions/{id}/chats?from&to&limit` | 채팅 구간 조회 (재생 동기화·문맥) |
| `POST /api/sessions/{id}/analyses`, `GET /api/analyses/{id}` | 분석·재분석·후보 |
| `GET /api/sessions/{id}/timeline` | 자동 후보 + 시청자 추천 통합 |
| `POST /api/sessions/{id}/exports` | 내보내기·checkpoint |
| `GET /api/sessions/{id}/playback` | HLS 정보 |
| `GET /api/ai/providers` | 제공자·모델 목록 |
| `POST /api/ai/preview`, `POST /api/ai/runs`, `GET /api/ai/runs/{id}` | AI 미리보기(예상 비용 포함)·실행·결과 |
| `GET /api/events` (SSE) | 작업 진행·새 채팅·후보 갱신 |

보안: 127.0.0.1 바인딩, 실행 시 생성한 로컬 접근 키, Host·Origin 검사 (기존 동작 유지). 접근 키는 `.momentforge/runtime/access-key`에도 기록해 E2E 스크립트가 읽을 수 있게 함.

## 9. 프론트엔드 (frontend-design 스킬 적용)

기술 구성

- Vite + React + TypeScript(strict), 서버 상태는 TanStack Query, 로컬 상태는 React 기본 기능.
- SSE 구독 훅 하나(`useEvents`)로 LIVE 채팅·후보·진행률 갱신.
- LIVE 채팅 목록은 가상 스크롤.
- HLS 재생은 `hls.js` npm 의존성으로 교체 (vendor 파일 제거).
- 화면 구성: 홈(URL 입력·저장 목록) / VOD 검토 / LIVE 모니터(다채널 탭) / AI 검토 / 설정(탐지 파라미터·AI 제공자).

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
python e2e/with_server.py --server "./bin/momentforge serve --no-browser --port 18765 --data e2e/.data" --port 18765 -- python e2e/test_demo.py
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
| `port-legacy-module` | 기존 Python 기능을 새 구조로 옮길 때 | 기존 동작 목록 작성 → 새 위치 결정(`references/legacy-map.md`) → 구현 → 기존 결과와 비교 → 체크리스트 갱신 |

예정 (해당 인터페이스가 실제로 생긴 뒤 작성):

| 스킬 | 만드는 시점 | 내용 |
|---|---|---|
| `add-platform` | 2단계 (치지직 어댑터 완성 후) | 새 플랫폼 어댑터 추가 절차: 인터페이스 구현, registry 등록, 응답 파일 기반 테스트, Capabilities 반영 |

작성 규칙: `SKILL.md`는 500줄 이하, 자세한 자료는 `references/`, 반복 실행 코드는 `scripts/`. 설명(description)에 언제 써야 하는지를 구체적으로 적음.

## 12. 단계별 계획

각 단계 끝에 **정상 작동 테스트(10절)**를 수행하고 통과해야 다음 단계로 넘어갑니다.

| 단계 | 내용 | 적용 스킬 | 완료 기준 |
|---|---|---|---|
| **0. 골격** ✅ | Go 모듈, chi 서버, 보안 미들웨어, SQLite·마이그레이션, Vite 앱 embed, 빌드 스크립트, `e2e/` 기반, `docs/DESIGN-SYSTEM.md` | frontend-design, webapp-testing, skill-creator(`e2e-check`) | 단일 바이너리 실행 → 브라우저에 앱 표시, E2E 1개 통과 |
| **1. 탐지 엔진** ✅ | 엔진 + 규칙 5종 + Params, 데모 데이터 | skill-creator(`add-detection-rule`) | 데모 데이터로 후보 생성, 기존 Python과 결과 비교 확인 |
| **2. VOD** | 치지직 VOD 메타·채팅 수집·이어받기, 분석, 후보 화면, HLS 재생·채팅 동기화, 내보내기 | frontend-design, webapp-testing, skill-creator(`add-platform`) | 실제 VOD URL로 수집 → 후보 클릭 → 해당 시각 재생 |
| **3. LIVE** | 치지직 LIVE WSS, 재접속·공백 기록, 증분 분석, checkpoint, 웹 시청자 수 보정, SSE | webapp-testing | 실제 생방송 30분 이상 수집, 중단·이어 기록 확인 |
| **4. 부가 기능** | 영상 댓글 추천, 통합 타임라인, 숨김/복원, 선택형 AI (기존과 같은 OpenAI) | webapp-testing | 기존 README 기능표 전 항목 동작 |
| **5. AI 강화 (확장 1순위)** | Claude 제공자(6.3절)를 기본으로 전환, 비용 미리보기, 배치 자동 분석·비용 한도, 결과 비교 UI, MCP 서버(6.5절) | claude-api, mcp-builder | 두 제공자로 같은 후보 검토, 캐시 적중 확인, MCP 평가 질문 10개 통과 |
| 6+. 이후 확장 | 탐지 파라미터 UI·규칙 추가 → LIVE 다채널·LIVE→VOD 연결 → 다른 플랫폼(SOOP·유튜브) | `add-detection-rule`, `add-platform` | 단계별 별도 계획 |

## 13. 기능 동등 체크리스트 (legacy 기준)

- [ ] VOD 채팅 페이지 수집·DB/TXT 저장·중단 위치 이어받기
- [x] 샘플로 먼저 체험(데모) — 분석·타임라인·후보·근거 표시 (1단계). 저장·내보내기는 2단계
- [ ] 공개 LIVE 채팅 수집·전체 대화 보관·최근 300건 표시
- [x] 자동 후보 D1~D5 판정, 임시 후보 표시 (1단계, 기존 결과와 필드 단위 일치)
- [ ] 소규모 채널 민감도 — 엔진 지원 완료, 화면 설정은 2단계
- [ ] 영상 댓글 타임스탬프 추천(수집·직접 붙여넣기)
- [ ] VOD 후보 클릭 → 재생 이동·재생 시각 채팅 따라가기
- [ ] LIVE 후보 앞뒤 30초 채팅 문맥(최대 300건)
- [ ] LIVE 자동 재연결·공백 표시·중단·같은 방송 이어 기록
- [ ] 내보내기: 전체 채팅 TXT/JSONL, 후보 TXT/CSV/JSON, 후보별 주변 채팅, checkpoint
- [ ] 웹 시청자 수 기록·10% 감소 시 기준 재학습
- [ ] 저장 목록 숨김·복원
- [ ] 선택형 AI(미리보기 확인 후 실행)
- [x] 127.0.0.1 바인딩·로컬 접근 키·Host·Origin 검사, 보안 헤더·CSP (0단계)
- [ ] 작성자 가명화

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

새로 결정이 필요한 사항이 생기면 이 표에 추가합니다.

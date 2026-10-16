---
name: momentforge-architecture
description: MomentForge refactoring and implementation rules. Use whenever writing, modifying, refactoring, or reviewing code in this repository (코드 작성·수정·리팩토링·리뷰) — Go packages under cmd/ and internal/, the React + TypeScript app under web/, SQLite migrations, MCP tools, or e2e tests. Enforces the package dependency direction, registry-based extension points, Go and TypeScript conventions, and privacy/security invariants, and says which upstream skill (claude-api, mcp-builder, frontend-design, webapp-testing, skill-creator) to load for each area. Run its layer checker after changing Go code.
license: MIT
compatibility: Designed for Claude Code. The layer checker needs Python 3.9+ (standard library only); gofmt and go vet checks need Go 1.23+.
metadata:
  project: momentforge
  design-doc: docs/REDESIGN.md
---

# MomentForge 구조 규칙

이 저장소의 모든 코드 변경에 적용합니다. 설계 근거는 `docs/REDESIGN.md`에 있으며, 이 스킬과 설계 문서가 다르면 설계 문서를 따르고 둘 중 하나를 고친 뒤 사용자에게 알립니다.

## 작업 절차

1. **영역 확인**: 바꿀 파일이 아래 표의 어느 영역인지 정합니다.
2. **영역 스킬 읽기**: 표에 적힌 스킬을 먼저 읽고 그 지침을 함께 따릅니다.
3. **규칙 적용**: 아래 핵심 규칙과 해당 영역의 references 문서를 적용합니다.
4. **구조 검사**: Go 파일을 바꿨다면 `python3 -I .claude/skills/momentforge-architecture/scripts/check_layers.py`를 실행하고 위반을 모두 고칩니다. Go가 설치되어 있으면 `gofmt -l`과 `go vet ./...`도 실행합니다.
5. **정상 작동 확인**: 단계가 끝났다면 webapp-testing 스킬로 E2E를 실행합니다 (REDESIGN.md 10절).
6. **기록**: 기능 동등 항목을 마쳤으면 REDESIGN.md 13절 체크리스트를, 설계와 다르게 결정했다면 15절 결정 기록을 갱신합니다.

| 영역 | 경로 | 먼저 읽을 스킬 | 상세 규칙 |
|---|---|---|---|
| 서버·도메인·저장소·탐지·플랫폼 | `cmd/`, `internal/` | — | [references/go.md](references/go.md) |
| AI 제공자 | `internal/ai/` | **claude-api** (Claude Code 내장) | [references/ai.md](references/ai.md) |
| MCP 서버 | `internal/mcp/` | **mcp-builder** | [references/go.md](references/go.md) 의 MCP 절 |
| 화면 | `web/` | **frontend-design** | [references/web.md](references/web.md) |
| E2E | `e2e/` | **webapp-testing** | [references/web.md](references/web.md) 의 테스트 절 |
| 프로젝트 스킬 | `.claude/skills/` | **skill-creator** | Agent Skills 규격 (agentskills.io/specification) |

개인정보·보안 규칙은 영역과 관계없이 항상 적용합니다: [references/privacy-security.md](references/privacy-security.md)

## 핵심 규칙

### 1. 의존 방향

Go 모듈 경로는 `github.com/manwol-ryu/momentforge`입니다. 패키지는 아래 표에 적힌 내부 패키지만 import할 수 있습니다. `check_layers.py`가 이 표를 그대로 검사합니다.

| 패키지 | import 가능한 내부 패키지 |
|---|---|
| `cmd/momentforge` | 모두 (구현체 등록·조립만 담당) |
| `internal/api` | `app`, `events`, `domain` |
| `internal/mcp` | `app`, `domain` |
| `internal/app` | `platform`, `detect`, `store`, `ai`, `export`, `events`, `privacy`, `domain` (각 패키지의 루트만) |
| `internal/platform` | `domain` |
| `internal/platform/<플랫폼>` | `platform`, `domain` |
| `internal/detect` | `domain` |
| `internal/detect/rules` | `detect`, `domain` |
| `internal/store`, `internal/export`, `internal/events` | `domain` |
| `internal/ai` | `domain` |
| `internal/ai/<제공자>` | `ai`, `domain` |
| `internal/privacy`, `internal/domain` | 없음 |

- `app`은 `platform/chzzk`, `ai/anthropic`, `detect/rules` 같은 **구현 패키지를 import하지 않습니다**. 인터페이스와 registry만 사용합니다.
- 여러 패키지가 함께 쓰는 타입(Session, ChatEvent, Candidate, Gap 등)은 `internal/domain`에 둡니다.

### 2. 확장은 등록으로

새 플랫폼·탐지 규칙·AI 제공자는 패키지를 하나 추가하고 `cmd/momentforge`에서 등록합니다. 기존 코드에 `switch platformID`, `if provider == "openai"` 같은 분기를 추가하지 않습니다.

```go
// cmd/momentforge/register.go
func registerAll(r *app.Registry) {
    r.Platforms.Register(chzzk.New())
    r.Rules.Register(rules.Laugh{}, rules.Surge{}, rules.Repeat{}, rules.Emotion{}, rules.Participants{})
    r.AI.Register(anthropic.New(), openai.New())
}
```

### 3. 위치 규칙 (리터럴 검사)

| 내용 | 둘 수 있는 곳 |
|---|---|
| 플랫폼 도메인·엔드포인트 (`chzzk.naver.com`, `naver.com` 등) | `internal/platform/<플랫폼>/` |
| Claude 모델 ID (`"claude-...`) | `internal/ai/anthropic/` |
| OpenAI 모델 ID·엔드포인트 | `internal/ai/openai/` |
| 탐지 임계값 숫자 | `internal/detect`의 `Params` 기본값 |
| `0.0.0.0` 바인딩 | 어디에도 두지 않음 |

### 4. 얇은 입구

`internal/api` 핸들러와 `internal/mcp` 도구는 입력 검증 → `app` 호출 → 응답 변환만 합니다. 수집·분석·저장 로직을 넣지 않습니다.

### 5. 동시성

- 장시간 작업은 `context.Context`로 취소할 수 있어야 합니다. goroutine을 만들면 종료 경로를 함께 만듭니다.
- 세션마다 DB writer goroutine은 하나입니다. 다른 goroutine은 채널로 쓰기 요청을 보냅니다.
- 채널 버퍼는 항상 상한을 둡니다 (LIVE 큐 10,000). 넘치면 조용히 버리지 말고 오류 상태로 알립니다.
- 진행 상황은 `events` 버스로 발행하고 화면은 SSE로 받습니다. 새 폴링을 만들지 않습니다.

### 6. 바꾸지 않을 것

- 기존 마이그레이션 파일은 수정하지 않습니다. 스키마 변경은 새 파일로 추가합니다.
- 보류 기능(본인 시청 계정 로그인 VOD, 공식 API 시청자 수)은 구현하지 않습니다.
- REDESIGN.md 5절 목록에 없는 외부 의존성은 사용자에게 먼저 확인합니다.

## 검사 스크립트

```bash
python3 -I .claude/skills/momentforge-architecture/scripts/check_layers.py
```

- 의존 방향(1절)과 위치 규칙(3절)을 검사합니다. Go 파일이 없으면 건너뜁니다.
- 위반이 있으면 파일·줄·이유·고치는 방법을 출력하고 종료 코드 1을 반환합니다.
- 규칙을 바꾸면 이 SKILL.md의 표와 스크립트의 `ALLOWED`, `LITERAL_RULES`를 함께 고칩니다.

# CLAUDE.md

MomentForge: 방송 채팅을 수집해 편집할 순간(후보)을 찾는 로컬 앱. CzzEdit(Python)를 포크해 **Go + React/TypeScript로 전면 재작성 중**이며, 이름도 MomentForge로 바꿨습니다. 실행파일·Go 모듈·데이터 폴더 이름은 `momentforge`입니다 (모듈 경로 `github.com/manwol-ryu/momentforge`).

- 설계 기준: [docs/REDESIGN.md](docs/REDESIGN.md). 구조·단계·완료 기준은 이 문서를 따르고, 설계를 바꾸면 문서도 함께 고칩니다.
- 기존 Python 코드: `legacy-python` 브랜치 (`git show legacy-python:czzedit/<파일>`로 읽음).
- 사용자 문서: [README.md](README.md)(사용법), [PRIVACY.md](PRIVACY.md)(외부 전송·로컬 저장 범위). 해당 동작을 바꾸면 함께 고칩니다.
- 보류한 기능은 구현하지 않습니다: 본인 시청 계정 로그인 VOD, 공식 API 시청자 수 기록.
- 사용자와는 한국어로 소통하고, 화면 문구도 한국어로 작성합니다. 코드 식별자와 커밋 메시지는 영어로 씁니다.

## 스킬 (자동 적용)

리팩토링·구현 규칙은 `.claude/skills/`의 스킬에 있습니다. 작업에 맞는 스킬을 **먼저 읽고** 그 절차를 따릅니다.

| 작업 | 스킬 |
|---|---|
| 이 저장소의 모든 코드 작성·수정·리팩토링·리뷰 | **momentforge-architecture** (구조 규칙, 영역별 상세 규칙, 검사 스크립트) |
| 기존 Python 기능을 새 구조로 옮기기 | **port-legacy-module** (동작 목록 → 구현 → 기존 결과와 비교) |
| `internal/ai`의 Claude 호출 코드 | **claude-api** (Claude Code 내장). SDK 사용법은 기억이 아니라 이 스킬의 Go 문서로 확인 |
| `internal/mcp` 도구 | **mcp-builder** |
| 화면 설계·구성 변경 | **frontend-design** |
| 단계 완료 후 정상 작동 확인 | **webapp-testing** |
| 탐지 규칙 추가·임계값 조정, 탐지 결과 설명 | **add-detection-rule** |
| 플랫폼 추가, 플랫폼 응답 변경 대응 | **add-platform** |
| 단계 완료·커밋 전 정상 작동 확인, E2E 실패 진단·추가 | **e2e-check** (`make test` → `make e2e` → 스크린샷 점검 → 보고) |
| 새 프로젝트 스킬 작성·수정 | **skill-creator** (Agent Skills 규격: agentskills.io/specification) |

외부 스킬 4개(frontend-design, webapp-testing, mcp-builder, skill-creator)는 anthropics/skills에서 고정 커밋으로 가져왔습니다. 출처와 갱신 방법은 `.claude/skills/UPSTREAM.md`에 있습니다. 이 4개는 직접 수정하지 않습니다.

**자동 검사**: `.claude/settings.json`의 PostToolUse 훅이 `cmd/`·`internal/`의 Go 파일을 수정할 때마다 `momentforge-architecture/scripts/check_layers.py`를 실행합니다. 의존 방향, 위치 규칙, gofmt를 검사합니다. 위반이 보고되면 다음 작업으로 넘어가기 전에 고칩니다. 규칙을 바꿀 때는 스킬 문서와 스크립트를 함께 고칩니다.

## 명령

명령이 바뀌면 이 절을 수정합니다. Go가 PATH에 없으면 Makefile이 `/usr/local/go/bin/go`를 씁니다.

```bash
make dev          # Go 서버 + Vite 개발 서버 동시 실행
make build        # web 빌드 → go:embed → bin/momentforge
make test         # 구조 검사 + gofmt + go vet + go test + 화면 타입 검사
make e2e          # 가짜 생방송을 넣은 bin/momentforge-e2e 빌드 후 e2e/ Playwright 정상 작동 테스트
python3 -I .claude/skills/momentforge-architecture/scripts/check_layers.py   # 구조 검사만
./bin/momentforge mcp --data /절대/경로/.momentforge   # MCP 서버(stdio, 읽기 전용). Claude Desktop·Code에 이 명령으로 등록
MF_MCP_EVAL_DATA=<빈 폴더> go test ./cmd/momentforge -run TestMCPEvalAnswers   # e2e/mcp-eval.xml용 평가 데이터 만들기
MF_CUT_EVAL_DATA=<데이터 폴더> go test ./cmd/momentforge -run TestCutEvalReport -v   # 컷 분석 평가(e2e/cut-eval/), 읽기 전용. MF_CUT_EVAL_DRAFT=<세션 ID>면 확정 컷에서 정답 초안
```

개발 환경: Go 1.26 이상, Node 20.19 이상, Python 3.9 이상(검사 스크립트·E2E). 실행 결과물은 `bin/momentforge` 하나이며, 사용자는 Go·Node·Python이 필요 없습니다.

실행 시 데이터 폴더는 현재 폴더의 `.momentforge/`입니다(`--data`로 변경). E2E는 `e2e/.data/`, 포트 18765를 쓰며, API 키 환경변수를 지운 채 서버를 띄웁니다.

## 항상 지킬 것

- 서버는 `127.0.0.1`에만 바인딩하고, 로컬 접근 키·Host·Origin 검사를 유지합니다.
- 채팅 작성자는 세션별 salt 가명 키로만 저장·표시합니다. 원본 ID·닉네임·프로필·접속 토큰은 저장하지 않습니다.
- API 키는 메모리에 둡니다. 사용자가 "이 컴퓨터에도 저장"을 고른 키만 데이터 폴더의 `.env`(0600)에 쓰고, DB·로그·브라우저 저장소에는 두지 않습니다. AI 전송은 사용자가 미리보기를 확인한 뒤에만 실행합니다.
- 수집 데이터(`.momentforge/`, `.czzedit/`, `e2e/.data/`)는 커밋하지 않습니다.
- 자세한 내용: `.claude/skills/momentforge-architecture/references/privacy-security.md`

## 작업 방식

- REDESIGN.md 12절의 단계 순서대로 진행합니다. 단계마다 완료 기준과 13절 체크리스트를 갱신합니다.
- 테스트는 구현 후에 작성하고, 단계가 끝나면 정상 작동 테스트를 실행합니다 (사용자 결정).
- 외부 서비스(치지직 실제 URL, AI API)가 필요한 확인은 사용자에게 알리고 진행합니다.
- 설계 문서에 없는 결정이 필요하면 사용자에게 먼저 확인하고, REDESIGN.md 15절 결정 기록에 추가합니다.
- 커밋과 push는 사용자가 요청할 때만 합니다.

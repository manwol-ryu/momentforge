# Go 규칙

`cmd/`, `internal/` 아래 코드에 적용합니다.

## 목차

1. 기본 도구
2. 패키지별 책임
3. 오류
4. 저장소와 마이그레이션
5. 플랫폼 어댑터
6. 탐지 엔진
7. MCP 서버
8. 테스트

## 1. 기본 도구

- Go 1.26 이상(`go.mod` 기준), `CGO_ENABLED=0`으로 빌드합니다. SQLite는 `modernc.org/sqlite`를 씁니다.
- 모든 파일은 `gofmt`를 통과해야 하고 `go vet ./...`에 경고가 없어야 합니다.
- 로그는 `log/slog`만 씁니다. `fmt.Println`·`log.Printf`로 운영 로그를 남기지 않습니다.
- 로그·오류 메시지에 채팅 본문, 원본 작성자 ID, API 키를 넣지 않습니다.

## 2. 패키지별 책임

| 패키지 | 책임 | 넣지 않을 것 |
|---|---|---|
| `domain` | 공용 타입, 값 검증 | I/O, 외부 의존성 |
| `platform` | `Platform`·`VODSource`·`LiveSource` 등 인터페이스, registry, `ErrSchemaChanged` | 특정 플랫폼 코드 |
| `platform/<플랫폼>` | URL 판별, HTTP·WebSocket 호출, 응답 해석 | 저장, 분석, 재접속 정책 |
| `detect` | 10초 창·5초 간격 평가, 기준 학습, 후보 병합·분할, `Params` | 특정 반응 판정 로직 |
| `detect/rules` | 규칙별 반응 키 추출과 판정 | 창 관리, 저장 |
| `store` | SQLite 연결, 마이그레이션, 조회·쓰기 | 분석, 외부 호출 |
| `app` | 유스케이스(수집·분석·내보내기·AI), 작업 관리, 재접속·백오프, 가명화 호출 | HTTP·MCP 형식 |
| `api` | 라우팅, 입력 검증, JSON·SSE 응답, 로컬 키·Host·Origin 검사 | 비즈니스 로직 |
| `export` | TXT·JSONL·CSV·JSON 생성, checkpoint | DB 쓰기 |

## 3. 오류

- 오류는 `fmt.Errorf("동작: %w", err)`로 감쌉니다. 비교는 `errors.Is`, `errors.As`로 합니다.
- 사용자에게 보여줄 오류는 `domain.UserError{Code, Message}`로 만듭니다. `Message`는 무슨 일이 생겼고 어떻게 하면 되는지 한국어로 적습니다. 기존 오류 코드(`LIVE_OFFLINE`, `LIVE_BUSY`, `SCHEMA_CHANGED` 등)는 그대로 사용합니다.
- 플랫폼 응답 형식이 예상과 다르면 `platform.ErrSchemaChanged`를 감싸서 반환합니다. 빈 값이나 0으로 대체해 계속 진행하지 않습니다.
- `panic`은 프로그래밍 오류에만 씁니다.

## 4. 저장소와 마이그레이션

- 마이그레이션은 `internal/store/migrations/NNNN_설명.sql`로 추가하고 `embed`로 포함합니다. `PRAGMA user_version`으로 적용 여부를 관리합니다.
- 이미 커밋한 마이그레이션 파일은 수정하지 않습니다.
- WAL 모드를 쓰고, 세션마다 writer goroutine 하나가 배치(약 1초 또는 200건)로 씁니다.
- DB가 원본입니다. TXT 등 출력 파일 생성이 실패해도 DB 쓰기를 되돌리지 않습니다.
- 쿼리는 항상 매개변수 바인딩을 씁니다. 문자열 연결로 SQL을 만들지 않습니다.

## 5. 플랫폼 어댑터

- 어댑터는 원본 작성자 ID를 `domain.ChatEvent.AuthorID`에 담아 반환만 합니다. `app`이 `privacy`로 세션별 salt 가명 키를 만들어 저장하고, 원본 ID는 저장하지 않습니다.
- 재접속·백오프·공백 기록은 `app`이 담당합니다. 어댑터의 `Stream`은 연결이 끊기면 오류를 반환하고 끝납니다.
- 응답 해석 테스트용 응답 파일은 `internal/platform/<플랫폼>/testdata/`에 둡니다. 닉네임·프로필 등 개인정보는 가짜 값으로 바꿔서 저장합니다.
- 지원 기능은 `Capabilities()`로 알립니다. 지원하지 않는 기능의 인터페이스는 구현하지 않습니다.

## 6. 탐지 엔진

- 임계값은 모두 `detect.Params` 필드로 두고, 기본값과 소규모 채널 프리셋을 함께 정의합니다.
- 통계는 10초 구간 단위로 증분 계산합니다. 평가할 때마다 과거 구간 전체를 다시 계산하지 않습니다.
- VOD와 LIVE는 같은 엔진을 씁니다. LIVE는 5초 지연한 평가 시각을 쓰고, 이미 확정한 판정을 소급해 바꾸지 않습니다.
- 연결 공백 구간은 기준 학습에 넣지 않습니다.
- 동작을 바꾸면 데모 데이터로 기존 Python 결과와 비교합니다 (port-legacy-module 스킬 참고).

## 7. MCP 서버

mcp-builder 스킬을 먼저 읽습니다. 이 프로젝트에서는 다음을 함께 지킵니다.

- 공식 Go SDK(`github.com/modelcontextprotocol/go-sdk`)와 stdio 전송을 씁니다.
- 도구 이름은 `mf_` 접두어 + 동작입니다 (`mf_list_sessions`, `mf_search_chats` 등).
- 모든 도구는 읽기 전용(`readOnlyHint: true`)입니다. 수집 시작·삭제 같은 도구는 만들지 않습니다.
- 목록·검색 도구는 `limit`, `cursor`를 받습니다. 기본 `limit`는 100 이하입니다.
- 오류 메시지에 다음에 호출할 도구를 적습니다.
- 평가 질문은 `e2e/mcp-eval.xml`에 mcp-builder의 형식으로 둡니다. 질문을 바꾸면 `cmd/momentforge/mcp_test.go`의 `TestMCPEvalAnswers`에 도구만으로 답을 구하는 풀이를 함께 고칩니다.
- MCP 서버는 `store.OpenReadOnly`로 DB를 읽기 전용으로 열고, `app`의 읽기 함수(`explore.go`)만 부릅니다. 키를 읽거나 AI 제공자를 등록하지 않습니다.
- `internal/mcp`의 테스트는 저장소를 직접 만들 수 없으므로(의존 방향), 데이터가 필요한 MCP 테스트는 `cmd/momentforge`에 둡니다.
- 도구 설명에 `limit`의 기본값·최댓값을 적고, 인자 오류는 `inputErr`로 그대로 보여 줍니다(내부 오류만 일반 문구로 가립니다).

## 8. 테스트

- 테스트는 구현 후에 작성합니다 (사용자 결정). 단계가 끝나면 `go test ./...`과 E2E를 실행합니다.
- 테스트에서 외부 네트워크를 호출하지 않습니다. 플랫폼은 `testdata/` 응답 파일, AI는 가짜 `ai.Provider`를 씁니다.
- 테이블 기반 테스트를 기본으로 합니다.

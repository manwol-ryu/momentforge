# AI 제공자 규칙

`internal/ai/`에 적용합니다. **Claude 호출 코드를 쓰기 전에 Claude Code에 내장된 claude-api 스킬을 반드시 읽습니다.** SDK 함수 이름, 매개변수, 모델별 제약은 기억이 아니라 그 스킬의 Go 문서(`go/claude-api/`)로 확인합니다. 아래 내용은 이 프로젝트에서 정한 사용 방식이며, claude-api 스킬과 다르면 claude-api 스킬이 맞습니다.

## 구조

- `internal/ai`: `Provider`와 선택 기능 인터페이스(`Priced`, `TokenCounter`, `ModelLister`, `BatchProvider`), registry, 지시문(`prompt.go`), 계획(`Prepare`: 요청 나누기·최대 비용·비용 한도), 출력 검증(`Validate`), 가격 계산(`cost.go`). 제공자와 무관한 코드만 둡니다.
- `internal/ai/anthropic`: 기본 제공자. `github.com/anthropics/anthropic-sdk-go` 사용.
- `internal/ai/openai`: 선택 제공자 (기존 기능 동등용).
- `internal/ai/opencode`: OpenCode Go 모델을 OpenCode CLI(`opencode run`)로 씁니다. 키는 OpenCode 로그인을 쓰는 `SelfAuthenticated` 제공자입니다. 요청마다 빈 임시 폴더의 설정으로 모든 도구를 거부하고(`steps: 1`), 채팅은 표준 입력으로 넘기며, 끝나면 OpenCode 세션을 지웁니다. 이 규칙을 바꾸면 REDESIGN.md 6.3절도 고칩니다. 테스트는 가짜 CLI 스크립트로만 하고 실제 `opencode`를 부르지 않습니다(E2E 실행파일은 없는 경로를 보게 함).
- 새 제공자는 `internal/ai/<제공자>/`를 추가하고 `cmd/momentforge`에서 등록합니다.

## Claude 사용 방식

| 항목 | 규칙 |
|---|---|
| 모델 | 기본 `claude-opus-5-5`. 모델 ID 상수는 `internal/ai/anthropic`에만 둡니다. 화면의 모델 목록은 Models API로 조회합니다 |
| effort | 항상 명시합니다. 후보 검토 `high`, 대량 1차 분류 `low` |
| 사고 | 모델 기본값(현재 모델은 적응형)을 씁니다. `budget_tokens`, 사고 비활성화는 쓰지 않습니다. 사고가 출력 상한을 함께 쓰므로 `MaxOutputTokens`는 답 JSON보다 넉넉하게(8,000) |
| 출력 | 구조화 출력(`output_config.format`, JSON 스키마). prefill과 강제 `tool_choice`는 쓰지 않습니다 |
| 캐싱 | 순서: 고정 시스템 프롬프트 → 세션 공통 자료(지금은 없음) → 후보별 채팅. 앞쪽에 `cache_control`. 시스템 프롬프트에 시각·무작위 값·정렬되지 않은 JSON을 넣지 않습니다. 지시문을 줄이면 캐시 최소 길이(512토큰) 아래로 내려가지 않는지 `TestPromptIsCacheable`로 확인합니다 |
| 캐시 확인 | `usage.cache_read_input_tokens`를 `ai_runs`에 기록합니다. 반복 요청에서 계속 0이면 원인을 찾습니다 |
| 긴 요청 | 입력·출력이 길면 스트리밍을 씁니다 |
| 미리보기 | 보낼 내용과 최대 비용(바이트 기반 상한 × 공시 가격)을 항상 보여줍니다. 토큰 카운트 API는 채팅을 보내므로 사용자가 "정확한 토큰 수 계산"을 누를 때만 부릅니다 |
| 자동 분석 | "모든 후보 자동 선택"은 비용 한도가 있어야 하고, 미리보기 확인 후에만 보냅니다. 배치 API를 고를 수 있으며 결과는 `custom_id`(`chunk-NNN`, 계획 안 순번)로 맞춥니다 |
| 오류 | `errors.As`로 `*anthropic.Error`를 꺼내 상태 코드로 분기합니다. 429·5xx·전송 전 연결 실패는 재시도, 400·404는 즉시 실패하고 원인을 화면에 표시합니다. 전송 뒤 결과를 모르면 재전송하지 않습니다. SDK 자동 재시도는 끕니다 |
| 클라이언트 | `option.WithoutEnvironmentDefaults()`로 환경변수 기본값(`ANTHROPIC_BASE_URL` 등)을 끄고, 사용자가 준 키만 씁니다 |
| 거절 | `refusal`은 `AI_REFUSAL`로 표시합니다. 서버 측 대체 모델(fallbacks)은 미리보기에서 확인하지 않은 모델로 보내게 되므로 켜지 않습니다 |
| 가격 | 가격표는 `internal/ai/anthropic`에만 두고 기준 시점(`AsOf`)을 함께 둡니다. 가격이 바뀌면 표와 REDESIGN.md 15절을 고칩니다 |

## 사용자 보호

- AI 전송은 사용자가 미리보기(보낼 채팅·댓글·근거, 예상 비용)를 확인한 뒤에만 실행합니다.
- 자동 분석은 사용자가 켜고 비용 한도를 정했을 때만 동작하며, 한도에 닿으면 멈추고 알립니다.
- API 키는 서버 메모리에만 둡니다. 환경변수 `ANTHROPIC_API_KEY`가 있으면 사용합니다. 파일·DB·로그에 쓰지 않습니다.
- 응답은 `Validate`로 스키마와 후보 ID를 확인한 뒤 저장합니다. 응답에 없는 시각이나 후보를 만들어 넣지 않습니다.

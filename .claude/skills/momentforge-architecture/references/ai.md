# AI 제공자 규칙

`internal/ai/`에 적용합니다. **Claude 호출 코드를 쓰기 전에 Claude Code에 내장된 claude-api 스킬을 반드시 읽습니다.** SDK 함수 이름, 매개변수, 모델별 제약은 기억이 아니라 그 스킬의 Go 문서(`go/claude-api/`)로 확인합니다. 아래 내용은 이 프로젝트에서 정한 사용 방식이며, claude-api 스킬과 다르면 claude-api 스킬이 맞습니다.

## 구조

- `internal/ai`: `Provider`, `BatchProvider` 인터페이스, registry, 프롬프트 생성(`Prepare`), 출력 검증(`Validate`). 제공자와 무관한 코드만 둡니다.
- `internal/ai/anthropic`: 기본 제공자. `github.com/anthropics/anthropic-sdk-go` 사용.
- `internal/ai/openai`: 선택 제공자 (기존 기능 동등용).
- 새 제공자는 `internal/ai/<제공자>/`를 추가하고 `cmd/momentforge`에서 등록합니다.

## Claude 사용 방식

| 항목 | 규칙 |
|---|---|
| 모델 | 기본 `claude-opus-5-5`. 모델 ID 상수는 `internal/ai/anthropic`에만 둡니다. 화면의 모델 목록은 Models API로 조회합니다 |
| effort | 항상 명시합니다. 후보 검토 `high`, 대량 1차 분류 `low` |
| 사고 | 적응형 사고(기본)를 씁니다. `budget_tokens`, 사고 비활성화는 쓰지 않습니다 |
| 출력 | 구조화 출력(`output_config.format`, JSON 스키마). prefill과 강제 `tool_choice`는 쓰지 않습니다 |
| 캐싱 | 순서: 고정 시스템 프롬프트 → 세션 공통 자료 → 후보별 채팅. 앞쪽에 `cache_control`. 시스템 프롬프트에 시각·무작위 값·정렬되지 않은 JSON을 넣지 않습니다 |
| 캐시 확인 | `usage.cache_read_input_tokens`를 `ai_runs`에 기록합니다. 반복 요청에서 계속 0이면 원인을 찾습니다 |
| 긴 요청 | 입력·출력이 길면 스트리밍을 씁니다 |
| 미리보기 | 실행 전 토큰 카운트 API로 입력 토큰과 예상 비용을 보여줍니다 |
| 자동 분석 | 급하지 않은 대량 작업은 배치 API를 씁니다. 결과는 `custom_id`(후보 ID)로 맞춥니다 |
| 오류 | `errors.As`로 `*anthropic.Error`를 꺼내 상태 코드로 분기합니다. 429·5xx·연결 오류는 재시도, 400·404는 즉시 실패하고 원인을 화면에 표시합니다 |

## 사용자 보호

- AI 전송은 사용자가 미리보기(보낼 채팅·댓글·근거, 예상 비용)를 확인한 뒤에만 실행합니다.
- 자동 분석은 사용자가 켜고 비용 한도를 정했을 때만 동작하며, 한도에 닿으면 멈추고 알립니다.
- API 키는 서버 메모리에만 둡니다. 환경변수 `ANTHROPIC_API_KEY`가 있으면 사용합니다. 파일·DB·로그에 쓰지 않습니다.
- 응답은 `Validate`로 스키마와 후보 ID를 확인한 뒤 저장합니다. 응답에 없는 시각이나 후보를 만들어 넣지 않습니다.

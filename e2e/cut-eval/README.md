# 컷 분석 평가 세트

AI 컷 분석이 만든 편집 구간을 사람이 적은 정답과 비교합니다 (설계: docs/AI-CUT-ANALYSIS.md 9절).
채점기(`internal/cuteval`)는 저장된 결과만 읽고 AI에 아무것도 보내지 않습니다. 컷 분석은 앱에서 미리보기를 확인하고 실행합니다.

## 사례 파일 (`cases/*.json`)

| 필드 | 뜻 |
|---|---|
| `id`, `title`, `tags` | 사례 이름과 시험하는 상황 (callback, donation_overlap, repeated_attempt, irony, dropped_negation, late_correction, collection_gap, no_ending …) |
| `session` | `{"demo": true}`(샘플), `{"video": "chzzk:15723059"}`(플랫폼:영상번호), `{"title": "..."}` 중 하나 |
| `moment` | 반응 시각. 이 시각을 품은(없으면 1분 안에서 가장 가까운) 자동 탐지 후보를 채점함 |
| `gold.start`, `gold.end` | 클립이 시작·끝나도 되는 범위 `{from, to}` |
| `gold.must_include` | 자르면 안 되는 구간 (앞선 맥락, 사건, 결말) |
| `gold.cause` | 진짜 원인의 근거가 있는 구간. 가장 유력한 원인의 지지 근거가 하나라도 여기 들면 맞음 |
| `gold.origin` | 이야기가 시작된 곳 (반응이 앞선 일을 가리킬 때). 없으면 생략하고, 그때는 기원을 주장하지 않아야 맞음 |
| `gold.ending` | 기대하는 결말 상태: confirmed, pending, cut_off, insufficient, unresolved |

시각은 밀리초 숫자나 `"3:40"`, `"1:02:03.5"` 같은 문자열로 적습니다.

## 실행

```bash
MF_CUT_EVAL_DATA=/절대/경로/.momentforge go test ./cmd/momentforge -run TestCutEvalReport -v
```

- 데이터 폴더는 읽기 전용으로 엽니다(앱이 켜져 있어도 됨). `MF_CUT_EVAL_REPORT=<파일>`이면 보고서를 그 파일에도 씁니다.
- 비교하는 방식: 탐지기 구간, 1세대 장면 설명, 컷 분석(AI 선택)과 그 계획의 짧게·맥락·전체. 컷 분석은 발언을 읽었는지(발언+채팅 / 채팅만)를 나눠 합계를 냅니다.
- 독립적 이해도(클립만 보고 이해되는지)는 사람이 따로 봅니다.

## 정답 초안 만들기

앱에서 컷을 확정한 뒤:

```bash
MF_CUT_EVAL_DATA=/절대/경로/.momentforge MF_CUT_EVAL_DRAFT=<세션 ID> go test ./cmd/momentforge -run TestCutEvalReport -v
```

확정한 컷마다 `drafts/`(Git에 올리지 않음)에 초안이 생깁니다. 모든 범위를 영상으로 확인해 고친 뒤 `cases/`로 옮기세요. 초안은 AI가 고른 구간에서 시작하므로 그대로 쓰면 AI에 유리하게 채점됩니다.

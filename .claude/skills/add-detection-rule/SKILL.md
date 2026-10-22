---
name: add-detection-rule
description: Add, change or tune a MomentForge detection rule (탐지 규칙 추가·수정·민감도 조정) — implement detect.MessageRule or detect.WindowRule in internal/detect/rules, put every threshold in detect.Params, register it in rules.All(), bump RulesVersion, and verify with Go tests, the pinned CzzEdit comparison, and E2E. Use when the user wants a new reaction type (박수, 응원, 특정 밈, 후원 반응 등), wants candidates found more or less often, asks to change a threshold, or asks why a moment was or was not detected.
license: MIT
compatibility: Designed for Claude Code. Needs Go 1.26+; E2E needs the e2e-check skill's setup.
metadata:
  project: momentforge
  design-doc: docs/REDESIGN.md
---

# 탐지 규칙 추가·조정

탐지는 **엔진**(`internal/detect`)과 **규칙**(`internal/detect/rules`)으로 나뉩니다. 10초 창, 5초 간격 평가, 기준선 학습, 후보 묶기·분할은 엔진이 맡습니다. 규칙은 "이 메시지·이 창이 반응인가"만 판단합니다. 규칙을 추가할 때 엔진 코드는 고치지 않습니다.

## 1. 어떤 종류의 규칙인가

| 종류 | 인터페이스 | 판단 대상 | 예 |
|---|---|---|---|
| 메시지 규칙 | `detect.MessageRule` (`Keys`, `Share`) | 메시지마다 반응 키를 붙이고, 같은 키를 쓴 작성자 비율로 판정 | 웃음, 감정, 반복, 새 리액션 어휘 |
| 창 규칙 | `detect.WindowRule` (`Fires`) | 창 전체의 채팅 수·작성자 수와 기준선 | 채팅 급증, 참여자 급증 |

메시지 규칙의 판정은 엔진이 공통으로 합니다. 같은 키를 쓴 작성자가 최소 인원 이상이고, 창 작성자 중 비율이 `Share` 이상이며, 기준선이 준비됐다면 평소 비율보다 `ReactionShareIncrease` 이상 높아야 합니다.

모든 신호보다 먼저 판정 시작 조건을 통과해야 합니다. 기본값은 고정이 아니라 자동입니다(`Engine.gate`, REDESIGN.md 6.2.1).
- 장기: 10초 채팅 수가 최근 2시간 평소(중앙값)의 3.75배 이상
- 단기: 10초에 채팅한 사람이 최근 15분 평소의 5배 이상
- 둘 중 하나를 넘으면 판정하고, 둘 다 상한 없이 최소 6건·3명입니다. 처음 3분은 판정하지 않습니다.
- 그때의 기준은 창과 후보 정점의 `gate`, `spikeGate`에 남습니다.
- 민감도를 켜거나 `CzzEditParams()`를 쓰면 고정값입니다. 판정 방식 자체를 바꿔야 하면 창 규칙으로 만들거나, 엔진 변경으로 보고 사용자와 먼저 상의합니다.

위 판정 시작 조건과 `Share`·`Fires`는 **LIVE 증분 엔진**의 방식입니다. 다시보기·NAS 백업·다시 분석(저장된 타임라인)은 **방송 전체 분석**(`detect.AnalyzeBroadcast`, REDESIGN.md 6.2.2)으로 판정합니다. 규칙 3.1.0부터 LIVE는 둘 다 씁니다(REDESIGN.md 6.2.3): 증분 엔진은 판정 시작 조건의 두 비율에 `EarlyGateScale`(0.67)을 곱한 **예비** 후보만 띄우고, 저장된 최근 70분 채팅에 방송 전체 분석을 다시 해 반응 5분 뒤 **5분 판정**, 30분 뒤 **확정**합니다(`detect.JudgeLive`). 판정 시작 조건을 바꾸면 예비 후보도 함께 바뀝니다.
- 메시지 규칙은 등록하면 측정 하나가 됩니다. 창마다 그 규칙의 키를 쓴 작성자 수(키별 최댓값)를 앞뒤 ±5분·±30분 중 조용한 쪽의 평소 값과 비교해 점수(z)를 냅니다. 3명 미만이면 세지 않습니다. 둘 이상이 함께 쓴 키는 "반응한 사람" 측정에도 들어갑니다.
- `Share`, 창 규칙의 `Fires`, 판정 시작 조건은 쓰지 않습니다. 새 창 규칙은 LIVE에만 적용되므로, 다시보기에도 필요하면 `broadcast.go`의 측정으로 넣을지 사용자와 먼저 상의합니다.
- 임계값은 `detect.BroadcastParams`(`Params.Broadcast`)에 있습니다. `DefaultBroadcastParams()`를 바꾸면 `TestDetectEvalReport`로 실제 다시보기에서 전후를 비교합니다(5절).

## 2. 구현

`internal/detect/rules/<규칙>.go`에 작성합니다. 기존 예시는 `rules.go`, 어휘는 `vocab.go`에 있습니다.

```go
// Cheer: many authors cheering at once.
type Cheer struct{}

func (Cheer) ID() string                    { return "cheer" }         // 키 접두어, ':' 금지
func (Cheer) Name() string                  { return "응원 반응" }      // 화면의 이유 표시
func (Cheer) Share(p detect.Params) float64 { return p.CheerShare }
func (Cheer) Keys(text string) []string {
	if cheerPattern.MatchString(text) {
		return []string{"cheer"} // 세부 구분이 필요하면 "cheer:<종류>"
	}
	return nil
}
```

- `Keys`가 받는 `text`는 이미 정규화되어 있습니다. 소문자이고, 공백이 하나로 정리됐고, 같은 글자 3회 이상은 2회로 줄어 있습니다. 그래서 패턴은 `ㅋ{2,}`처럼 2회 기준으로 씁니다.
- Go 정규식(RE2)은 앞뒤 조건(lookaround)과 역참조를 지원하지 않습니다. 단어 경계가 필요하면 `vocab.go`의 `hasStandaloneWord`를 씁니다.
- 반복 규칙은 "순수 반응" 메시지를 제외합니다. 새 어휘가 순수 반응이라면 `vocab.go`의 `pureReaction`·`pureWords`에도 넣어야 반복 후보와 겹치지 않습니다.

## 3. 임계값은 Params에

- 숫자는 규칙 코드에 직접 쓰지 않습니다. `detect.Params`에 필드를 추가하고 JSON 태그를 붙입니다.
- 기본값은 `DefaultParams()`에서 정합니다. **`CzzEditParams()`는 고치지 않습니다.** 기존 결과와 비교할 때 쓰는 고정값입니다.
- 사용자가 조정할 수 있는 값이면 `Params.Validate()`에 범위 검사와 한국어 오류 문구를 추가합니다.
- 소규모 채널 민감도에서 다르게 동작해야 하면 `Gate()`처럼 `p.Sensitive`에 따라 값을 고르는 메서드를 둡니다.

## 4. 등록과 버전

1. `rules.All()`에 추가합니다. **`rules.CzzEdit()`에는 넣지 않습니다.** `cmd/momentforge`가 `rules.All()`을 앱에 넘기므로 다른 등록은 필요 없습니다.
2. 같은 입력의 분석 결과가 달라지므로 `detect.RulesVersion`을 올립니다. 규칙 추가나 기본값 변경은 minor, 판정 방식 변경은 major입니다.
3. REDESIGN.md 15절 결정 기록에 무엇을 왜 바꿨는지 추가합니다.

## 5. 확인

1. `rules_test.go`의 `TestMessageKeys`에 맞는 예와 맞지 않는 예를 추가합니다. 비슷하지만 걸리면 안 되는 문장(예: "대박이네")도 넣습니다.
2. 버스트가 들어간 합성 채팅으로 `detect.Analyze`를 돌려, 기대한 시각에 새 신호가 나오는지 테스트합니다.
3. `go test ./internal/detect/...`를 실행합니다. `TestMatchesLegacy`는 `CzzEdit()`와 `CzzEditParams()`를 쓰므로 계속 통과해야 합니다. 실패하면 엔진이나 기존 규칙을 건드린 것입니다.
4. 방송 전체 분석에도 들어가는지 `broadcast_test.go`처럼 `detect.AnalyzeBroadcast`로 확인합니다. 실제 데이터가 있으면 `MF_DETECT_EVAL_DATA=<데이터 폴더 절대 경로> go test ./cmd/momentforge -run TestDetectEvalReport -v`로 후보 수, 규모 변형 유지율, 섞은 채팅의 후보, 라벨(`e2e/detect-eval/labels/`) 일치율을 전후 비교합니다.
5. e2e-check 스킬로 `make test`와 `make e2e`를 실행합니다. 데모 결과(후보 3개)가 바뀌는 것이 의도된 변경이면 `e2e/test_demo.py` 기대값을 고치고 이유를 결정 기록에 남깁니다.

## 왜 잡혔는지·안 잡혔는지 설명할 때

먼저 분석의 `rulesVersion`을 봅니다. 3.x의 저장 분석(다시보기)은 방송 전체 분석입니다.

1. 후보의 `measures`가 정점의 측정값(`observed`), 그때 평소 값(`usual`), 드문 정도(`z`), 창 길이(`windowMs`, 10초 또는 20초)를 보여 줍니다. `score`는 그중 가장 높은 반응, 채팅·사람 급증 ×0.85, 반응과 급증을 합친 값 중 최댓값입니다.
2. 안 잡혔다면 그 시각 창들의 `score`를 봅니다. 정점이 `OnZ`(3, "항상 가장 민감하게"는 2.6) 미만이면 후보가 아닙니다. 반응 측정은 같은 반응을 한 사람이 3명 이상이어야 셉니다.
3. 평소 값이 높아서인지 봅니다. 앞뒤 ±5분과 ±30분 중 낮은 쪽과 비교하므로, 둘 다 활발한 구간이면 더 큰 반응이어야 합니다. 그 방송이 원래 뭉쳐서 반응하는 정도(분산)도 반영됩니다.
4. 강도는 점수와 순위 비율입니다(강함 = 4 이상·상위 20%, 보통 = 3.4 이상·상위 50%).

아래는 LIVE와 2.x 이하 저장 분석(증분 엔진)의 설명 순서입니다.

`detect.Analyze` 결과의 `Windows`에 5초마다 `n`(셈한 채팅 수), `u`(작성자 수), `ratio`, `z`, `baselineReady`, `signals`, `reactionRatios`가 있습니다. 해당 시각 앞뒤 창을 보고 다음 순서로 확인합니다.

1. 창이 무효인가? 연결 공백이나 작성자 정보 없는 채팅이 있으면 무효입니다.
2. 판정 시작 조건을 넘었나? 자동 기준이면 창의 `n`이 `gate` 이상(작성자도 같은 비율)이거나 `u`가 `spikeGate` 이상이어야 합니다. 둘 다 없으면 처음 3분 학습 중입니다. 고정 기준이면 `MinMessages`, `MinAuthors`입니다.
3. 기준선이 준비됐나? 유효한 10초 칸이 18개 이상 필요합니다.
4. 비율과 평소 대비 증가폭이 기준을 넘었나?
5. 신호가 연속으로 나오지 않았고 강한 신호도 아니어서 후보로 묶이지 않았나?

작성자 한 명당 창마다 3건까지만 세므로, 한 사람이 도배한 반응은 후보가 되지 않습니다.

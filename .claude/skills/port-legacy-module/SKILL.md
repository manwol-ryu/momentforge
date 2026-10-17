---
name: port-legacy-module
description: Port (refactor) a feature of the legacy CzzEdit Python app on the legacy-python branch into the new MomentForge Go + React structure. Use when the user asks to 이식·포팅·옮기기·리팩토링 a legacy feature, mentions a czzedit/*.py or czzedit/*.js file, or starts a phase in docs/REDESIGN.md that replaces legacy behavior (VOD 수집, 탐지, LIVE, 댓글, 내보내기, AI 등). Extracts the legacy behavior first, maps it to target packages, implements it under momentforge-architecture rules, and verifies parity.
license: MIT
compatibility: Designed for Claude Code. Needs git with the legacy-python branch; legacy comparison runs need Python 3.10+.
metadata:
  project: momentforge
  design-doc: docs/REDESIGN.md
---

# 기존 코드 이식

기존 Python 코드를 한 줄씩 번역하지 않습니다. **동작을 먼저 목록으로 뽑고**, 새 구조에 맞게 다시 구현한 뒤, 같은 동작을 하는지 확인합니다.

## 절차

### 1. 대상 정하기

[references/legacy-map.md](references/legacy-map.md)에서 옮길 기존 파일과 새 위치, 해당 단계를 찾습니다. 지금 단계(REDESIGN.md 12절)보다 뒤 단계의 기능이면 사용자에게 먼저 확인합니다. 보류 기능(로그인 VOD, 공식 API 시청자 수)은 옮기지 않습니다.

### 2. 기존 동작 읽기

```bash
git show legacy-python:czzedit/<파일>
git grep -n "<함수명>" legacy-python -- czzedit/
```

main에는 `czzedit/`가 없습니다(2026-10-17 정리). 항상 `legacy-python` 브랜치를 읽습니다.

### 3. 동작 목록 작성

코드를 쓰기 전에 아래 형식으로 동작 목록을 만들어 사용자에게 보여줍니다. 이 목록이 이식 범위이자 확인 기준입니다.

```markdown
## <기능> 동작 목록 (legacy-python:czzedit/<파일>)
- 입력: ...
- 출력·저장: ...
- 상수·임계값: ... (→ Params 또는 설정으로 옮김)
- 오류 코드와 문구: LIVE_OFFLINE "현재 공개 채팅을 받을 수 있는 생방송이 아닙니다." ...
- 경계 상황: 빈 응답, 형식 변경, 중복 메시지, 늦게 도착한 채팅 ...
- 의도적으로 바꿀 점: ... (이유)
```

### 4. 구현

1. momentforge-architecture 스킬을 읽고 그 절차를 따릅니다. 영역에 맞는 상위 스킬(claude-api, mcp-builder, frontend-design)도 함께 읽습니다.
2. 기존 코드의 구조를 새 구조에 맞춰 나눕니다.

| 기존 코드에 섞여 있던 것 | 새 위치 |
|---|---|
| 플랫폼 URL·응답 해석 | `internal/platform/chzzk` |
| 숫자 임계값 | `internal/detect` `Params` 또는 `app` 설정 |
| 스레드·잠금·재접속 | `internal/app` (goroutine + context) |
| JSON `body` 컬럼 | `internal/store` 정규화된 테이블 + 마이그레이션 |
| HTTP 경로 분기 | `internal/api` 라우터 |
| 화면 DOM 조작 | `web/src/features/<기능>` 컴포넌트 |

3. 한국어 오류 문구와 오류 코드는 기존 것을 그대로 씁니다. 바꾸려면 동작 목록의 "의도적으로 바꿀 점"에 적습니다.
4. Go 파일을 바꿨으면 구조 검사를 실행합니다.

```bash
python3 -I .claude/skills/momentforge-architecture/scripts/check_layers.py
```

### 5. 동작 확인

- 동작 목록의 항목마다 확인 방법을 정하고 결과를 기록합니다: Go 테스트(`testdata/` 응답 파일), E2E(webapp-testing 스킬), 또는 기존 코드와 비교.
- **탐지·분석을 옮겼다면 기존 결과와 비교**합니다. 기존 코드를 임시 폴더에 꺼내 같은 입력으로 실행합니다.

```bash
LEGACY=$(mktemp -d) && git archive legacy-python czzedit | tar -x -C "$LEGACY"
python3 -I .claude/skills/port-legacy-module/scripts/legacy_detect.py --legacy "$LEGACY" \
    --events case.events.json --end 600000 [--config '{"sensitive": true}'] [--gaps '[[a, b]]']
```

  `scripts/legacy_detect.py`는 기존 탐지기를 실행해 새 키 이름(laugh, surge…)으로 결과를 출력합니다. 탐지 비교 데이터와 생성기는 `internal/detect/rules/testdata/legacy/`에 있습니다 (`gen_cases.py`, `TestMatchesLegacy`).

  후보 시작·끝 시각, 규칙, 개수를 비교합니다. 차이가 있으면 원인(중앙값 계산, 반올림, 정렬 등)을 찾아 고치거나, 의도한 차이면 이유를 기록합니다.
- 외부 서비스(치지직 실제 URL, AI API)가 필요한 확인은 사용자에게 알리고 진행합니다.

### 6. 기록과 보고

- REDESIGN.md 13절 기능 동등 체크리스트에서 끝난 항목을 체크합니다.
- 의도적으로 동작을 바꿨다면 REDESIGN.md 15절 결정 기록에 추가합니다.
- references/legacy-map.md의 해당 행 상태를 `완료`로 바꿉니다.
- 사용자에게 보고합니다: 옮긴 동작, 바뀐 동작과 이유, 확인한 방법, **확인하지 못한 항목**.

---
name: e2e-check
description: Run MomentForge's 정상 작동 테스트 (normal-operation check) and report the result — `make test` (structure checker, gofmt, go vet, go test, web typecheck) followed by `make e2e` (builds the single binary, starts it with with_server.py, runs the Playwright tests in e2e/). Use whenever a phase or feature from docs/REDESIGN.md is finished, before committing code changes, when the user asks to test, check or verify (테스트·확인·검증) that the app works, when an E2E test fails and needs diagnosing, or when adding a new E2E test.
license: MIT
compatibility: Designed for Claude Code on macOS/Linux. Needs Go 1.26+, Node 20.19+, Python 3.9+, make. The first e2e run downloads Playwright Chromium (about 92 MB).
metadata:
  project: momentforge
  design-doc: docs/REDESIGN.md
---

# 정상 작동 테스트

REDESIGN.md 10절의 확인 절차입니다. 단계마다 "완료"라고 말하기 전에 이 절차를 끝까지 실행하고, 결과를 있는 그대로 보고합니다.

## 실행 순서

1. `make test` — 빠른 검사부터 실행합니다. 구조 규칙, gofmt, go vet, Go 테스트, 화면 타입 검사입니다. 실패하면 E2E로 넘어가지 말고 먼저 고칩니다.
2. `make e2e` — 화면을 빌드하고 실행파일을 만든 뒤, `e2e/with_server.py`가 `bin/momentforge`를 포트 18765, 데이터 폴더 `e2e/.data`로 띄우고 `e2e/run.py`가 `e2e/test_*.py`의 모든 테스트를 실행합니다.
3. 화면을 바꿨다면 `e2e/.data/screenshots/`의 스크린샷(`home-light.png`, `home-dark.png`, `home-mobile.png` 등)을 Read 도구로 직접 보고 점검합니다. 한글 줄바꿈, 좁은 화면 넘침, 다크 모드 대비를 확인하고 `docs/DESIGN-SYSTEM.md`와 다르면 고칩니다.

처음 실행이면 `make e2e`가 `e2e/.venv`를 만들고 Chromium을 내려받습니다. 내려받기 전에 사용자에게 알립니다.

## 실패했을 때

| 증상 | 원인과 조치 |
|---|---|
| `check_layers.py` 위반 | 출력의 "고치는 방법"을 따릅니다. 규칙 자체를 바꿔야 하면 momentforge-architecture 스킬과 사용자 확인을 거칩니다 |
| `gofmt가 필요한 파일` | `gofmt -w <파일>` |
| `go test` 실패 | 실패한 테스트 이름과 메시지로 원인을 찾습니다. 테스트를 지우거나 기대값을 근거 없이 바꾸지 않습니다 |
| `tsc` 오류 | 타입을 고칩니다. `any`나 `@ts-ignore`로 덮지 않습니다 |
| `Server ... did not start` | 포트 18765를 다른 프로세스가 쓰는지 확인합니다 (`lsof -i :18765`). 남아 있는 `bin/momentforge`가 있으면 종료합니다 |
| E2E `실패` | 출력의 예외 메시지와 `스크린샷:` 경로의 실패 화면을 함께 봅니다. `브라우저 콘솔 오류`도 실패로 처리됩니다 |
| `data-app-state` 대기 시간 초과 | 화면이 초기 데이터를 받지 못했습니다. 실패 스크린샷의 오류 문구와 `/api/status` 응답을 확인합니다 |

외부 서비스(치지직 실제 URL, AI API)가 필요한 확인은 기본 E2E에 넣지 않습니다. 필요하면 사용자에게 알리고 따로 실행합니다.

## E2E 테스트 추가

- 파일: `e2e/test_<기능>.py`. 함수 이름은 `test_`로 시작하고, `(page, base_url)`을 받습니다.
- 페이지를 연 뒤 `body[data-app-state="ready"]`를 기다립니다. SSE 연결이 계속 열려 있어 `networkidle`은 오지 않습니다.
- 요소는 `get_by_role`, `get_by_text`로 찾습니다. 찾을 수 없으면 화면 코드에 역할과 접근 가능한 이름을 먼저 붙입니다.
- 데이터는 데모 데이터나 API로 준비하고, 테스트끼리 순서에 의존하지 않게 합니다. `make e2e`는 실행할 때마다 `e2e/.data`를 지웁니다.
- 특정 테스트만 실행: 서버를 띄운 상태에서 `e2e/.venv/bin/python e2e/run.py --base-url http://127.0.0.1:18765 -k <이름 일부>`

## 보고 형식

```markdown
## 정상 작동 테스트 결과
- make test: 통과 / 실패 (실패 항목)
- make e2e: N/M 통과 (실패 테스트와 원인)
- 화면 점검: 확인한 스크린샷과 고친 점
- 확인하지 못한 것: 외부 서비스가 필요한 항목 등
```

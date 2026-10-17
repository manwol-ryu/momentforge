# 화면·E2E 규칙

`web/`과 `e2e/`에 적용합니다. 화면을 새로 만들거나 구성을 바꿀 때는 frontend-design 스킬을, E2E를 작성·실행할 때는 webapp-testing 스킬을 먼저 읽습니다.

## 목차

1. 기술 구성
2. 디자인 시스템
3. 접근성과 테스트 표시
4. 화면 문구
5. E2E

## 1. 기술 구성

- Vite + React + TypeScript `strict`. `any`를 쓰지 않습니다. 서버 응답 타입은 `web/src/api/types.ts`에 둡니다.
- 서버 상태는 TanStack Query로만 가져옵니다. 컴포넌트에서 `fetch`를 직접 호출하지 않습니다.
- 실시간 데이터는 `useEvents` 훅 하나로 SSE를 구독합니다. `setInterval` 폴링을 만들지 않습니다.
- 기능별 폴더: `web/src/features/{home,session,live}/`. 기능끼리 서로 import하지 않고, 공용 코드는 `web/src/shared/`에 둡니다. 앱 틀(작업 목록 사이드바·휴대폰 서랍)은 `web/src/app/`에 두며 `shared`만 씁니다. 화면을 고르고 틀에 넣는 일은 `main.tsx`만 합니다.
- 긴 채팅 목록은 가상 스크롤을 씁니다.
- HLS 재생은 npm `hls.js`를 씁니다.
- 빌드 결과(`web/dist`)는 Go `embed`로 실행파일에 들어갑니다. 실행 중에 외부 CDN에서 스크립트·서체를 불러오지 않습니다 (로컬 앱 원칙).

## 2. 디자인 시스템

- 화면을 만들기 전에 `docs/DESIGN-SYSTEM.md`가 있는지 확인합니다. 없으면 frontend-design 스킬의 절차(계획 → 기본값 점검 → 구현 → 스크린샷 점검)로 먼저 작성하고 사용자에게 확인받습니다.
- 색·서체·간격·모서리 반경은 디자인 시스템의 CSS 변수로만 씁니다. 컴포넌트에 hex 값이나 임의의 px 값을 직접 쓰지 않습니다.
- 라이트·다크 모드를 모두 지원합니다.
- 눈에 띄는 요소는 반응 밀도 타임라인 하나로 두고, 나머지는 절제합니다. 영역은 Tracwell식 카드 셸(`.card` > `.card-head` + `.card-body`), 시트(`.sheet`, `.panel-section`), 떠 있는 틀(`.frame`)로 나누고 새 카드 모양을 만들지 않습니다. 출처 색은 자동 탐지 `--marker`, 시청자 추천 `--viewer`, AI `--ai`로 마커·구간·배지·선택 행에 같게 씁니다.
- 움직임은 상태 변화(사용자 동작, 새 데이터, 진행 중인 작업)에만 씁니다. 장식용 반복 애니메이션은 넣지 않습니다. 토큰(`--ease-*`, `--dur-*`)과 `web/src/shared/motion/`의 공용 컴포넌트(`Segmented`, `Tabs`, `Stats`, `RollingNumber`, `RevealWords`)를 먼저 씁니다. 규칙과 적용 목록은 `docs/DESIGN-SYSTEM.md` 8절에 있습니다.
- `prefers-reduced-motion`을 지킵니다. 전역 규칙이 길이·지연·반복을 없애므로, 새 애니메이션도 그 규칙에 기대도록 `animation`/`transition` 속성으로 만듭니다(JS 타이머로 움직이지 않음).

## 3. 접근성과 테스트 표시

- 버튼·입력·탭에는 의미에 맞는 요소(`button`, `input`, `role="tab"`)와 접근 가능한 이름을 씁니다. E2E는 `get_by_role`로 찾습니다.
- 키보드 포커스가 보여야 합니다. 클릭 가능한 `div`를 만들지 않습니다.
- 초기 데이터 로딩이 끝나면 `document.body.dataset.appState = "ready"`를 설정합니다. 오류가 나면 `"error"`로 설정합니다.
- `data-testid`는 역할과 이름으로 찾을 수 없는 경우에만 씁니다.

## 4. 화면 문구

- 한국어, 평서체, 짧게 씁니다.
- 기능 이름이 아니라 사용자가 하는 일로 씁니다: "WebSocket 재연결" 대신 "연결이 끊겨 다시 연결하는 중".
- 버튼은 결과를 말합니다: "수집 시작", "후보 내보내기". 같은 동작은 화면 전체에서 같은 이름을 씁니다.
- 오류는 원인과 해결 방법을 함께 적습니다. 사과 표현을 넣지 않습니다.
- 빈 화면에는 다음에 할 일을 안내합니다.

## 5. E2E

- 위치: `e2e/`. webapp-testing 스킬의 `scripts/with_server.py`를 `e2e/with_server.py`로 복사해 쓰고, 파일 맨 위에 출처(anthropics/skills, Apache-2.0)를 적습니다.
- 서버는 실제 실행파일로 띄웁니다: `./bin/momentforge serve --no-browser --port 18765 --data e2e/.data`
- 접근 키는 `e2e/.data/runtime/access-key`에서 읽습니다.
- 순서: 페이지 열기 → `body[data-app-state="ready"]` 대기 → 스크린샷·DOM 확인 → 찾은 선택자로 조작. SSE 연결이 계속 열려 있어 `networkidle`은 기다리지 않습니다.
- Chromium은 headless로 실행하고, 콘솔 오류가 하나라도 있으면 실패로 처리합니다.
- 테스트는 움직임 줄이기(`reduced_motion="reduce"`) 환경에서 돌립니다. 움직임을 확인하는 테스트만 `page.emulate_media(reduced_motion="no-preference")`로 켭니다(`test_motion.py`).
- 기본 E2E는 데모 데이터만 씁니다. 치지직·AI 같은 외부 서비스 확인은 사용자에게 알리고 따로 실행합니다.
- `e2e/.data/`는 커밋하지 않습니다.

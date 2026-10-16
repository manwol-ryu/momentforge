# 기존 파일 → 새 위치

기준: `legacy-python` 브랜치 `czzedit/` (CzzEdit 0.3.2). 단계는 REDESIGN.md 12절 기준입니다. 이식을 마치면 상태를 `완료`로 바꿉니다.

| 기존 파일 | 주요 내용 | 새 위치 | 단계 | 상태 |
|---|---|---|---|---|
| `__main__.py` | 실행 명령, 인자 처리 | `cmd/momentforge` | 0 | 완료 |
| `server.py` | HTTP 서버, 경로 분기, 로컬 키·Host·Origin 검사, 작업 상태 | `internal/api`, `internal/app` | 0, 2~4 | 진행 중 (보안·정적 파일·상태 완료) |
| `ui.html` | 화면 전체 | `web/` | 0, 2~4 | 진행 중 (홈 골격) |
| `common.py` | `Config`, `AppError`, `video_id`, `normalized` | `internal/domain`, `internal/detect` `Params`, `internal/platform/chzzk` | 1~2 | 진행 중 (`Config`→`Params`, `normalized`→`detect.Normalize`, `AppError`→`domain.UserError` 완료, `video_id`는 2단계) |
| `records.py` | 스키마 버전, records·viewer_samples 테이블 | `internal/store/migrations` | 0, 2~3 | 진행 중 (마이그레이션 체계 완료, 테이블은 해당 기능 단계에서) |
| `detector.py` | D1~D5 규칙, 창 통계, 후보 병합·분할 | `internal/detect`, `internal/detect/rules` | 1 | 완료 (기존 결과와 필드 단위 일치, 시청자 수 보정 연결은 3단계) |
| `live_analyzer.py` | LIVE 증분 평가 | `internal/detect` (같은 엔진) | 1, 3 | 진행 중 (엔진의 증분 API 완료, 대기·확정 상태 처리는 3단계) |
| `transport.py` | VOD HTTP 조회, 재시도 간격 | `internal/platform/chzzk` (로그인 전송은 보류) | 2 | 대기 |
| `collector.py` | VOD 채팅 페이지 수집·이어받기 | `internal/platform/chzzk` `VODSource`, `internal/app` | 2 | 대기 |
| `store.py` | VOD 세션·페이지·이벤트·분석 저장, 작업 잠금 | `internal/store` | 2 | 대기 |
| `service.py` | 수집·데모·재분석 흐름 | `internal/app` | 1~2 | 진행 중 (`demo_rows`→`domain.DemoChat` 완료, 수집·재분석은 2단계) |
| `exporter.py` | TXT·CSV·JSON 내보내기 | `internal/export` | 2 | 대기 |
| `review.py` | 후보 문맥, 재생 시각 채팅 | `internal/app`, `internal/api` 채팅 조회 | 2 | 대기 |
| `playback.py` | HLS 주소 | `internal/platform/chzzk` `PlaybackSource` | 2 | 대기 |
| `player.js` | 영상 재생·채팅 동기화 | `web/src/features/vod` (npm `hls.js`) | 2 | 대기 |
| `vendor/hls.min.js` | HLS 라이브러리 | npm `hls.js`로 대체 | 2 | 대기 |
| `live_transport.py` | LIVE 정보 조회, 익명 토큰, READ WebSocket | `internal/platform/chzzk` `LiveSource` | 3 | 대기 |
| `live_service.py` | LIVE 작업, 큐, 재접속·백오프, 중단·이어 기록 | `internal/app` | 3 | 대기 |
| `live_store.py` | LIVE 세션·구간·이벤트·후보·변경 저장 | `internal/store` (VOD와 통합) | 3 | 대기 |
| `live.js` | LIVE 화면 | `web/src/features/live` | 3 | 대기 |
| `viewers.py` | 웹 시청자 수 보정 | `internal/detect` (기준 재학습), `internal/platform/chzzk` `ViewerSource` | 3 | 대기 |
| `viewers.py` | 공식 API 시청자 수 | — | 보류 | 보류 |
| `comments.py` | 영상 댓글 수집, 타임스탬프 추출 | `internal/platform/chzzk` `CommentSource`, `internal/app` | 4 | 대기 |
| `timeline.py` | 자동 후보 + 시청자 추천 통합 | `internal/app` | 4 | 대기 |
| `v2.js` | 통합 타임라인 화면 | `web/src/features/timeline` | 4 | 대기 |
| `history.py` | 저장 목록 숨김·복원 | `internal/store`, `internal/app` | 4 | 대기 |
| `ai.py` | 미리보기, OpenAI 호출, 결과 검증 | `internal/ai`, `internal/ai/openai` (5단계에 `internal/ai/anthropic` 추가) | 4, 5 | 대기 |

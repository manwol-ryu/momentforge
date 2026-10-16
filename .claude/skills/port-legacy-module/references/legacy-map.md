# 기존 파일 → 새 위치

기준: `legacy-python` 브랜치 `czzedit/` (CzzEdit 0.3.2). 단계는 REDESIGN.md 12절 기준입니다. 이식을 마치면 상태를 `완료`로 바꿉니다.

| 기존 파일 | 주요 내용 | 새 위치 | 단계 | 상태 |
|---|---|---|---|---|
| `__main__.py` | 실행 명령, 인자 처리 | `cmd/momentforge` | 0 | 완료 |
| `server.py` | HTTP 서버, 경로 분기, 로컬 키·Host·Origin 검사, 작업 상태 | `internal/api`, `internal/app` | 0, 2~4 | 완료 |
| `ui.html` | 화면 전체 | `web/` | 0, 2~4 | 완료 |
| `common.py` | `Config`, `AppError`, `video_id`, `normalized` | `internal/domain`, `internal/detect` `Params`, `internal/platform/chzzk` | 1~2 | 완료 |
| `records.py` | 스키마 버전, records·viewer_samples 테이블 | `internal/store/migrations` | 0, 2~3 | 완료 (viewer_samples는 0003) |
| `detector.py` | D1~D5 규칙, 창 통계, 후보 병합·분할 | `internal/detect`, `internal/detect/rules` | 1, 3 | 완료 (기존 결과와 필드 단위 일치, 시청자 수 보정 연결 포함) |
| `live_analyzer.py` | LIVE 증분 평가 | `internal/detect` `LiveAnalyzer` (같은 엔진) | 1, 3 | 완료 (기존 결과와 후보 필드 단위 일치, `live_*.steps.json`) |
| `transport.py` | VOD HTTP 조회, 재시도 간격 | `internal/platform/chzzk` (로그인 전송은 보류) | 2 | 완료 |
| `collector.py` | VOD 채팅 페이지 수집·이어받기 | `internal/platform/chzzk` `VODSource`, `internal/app` | 2 | 완료 |
| `store.py` | VOD 세션·페이지·이벤트·분석 저장, 작업 잠금 | `internal/store` | 2 | 완료 |
| `service.py` | 수집·데모·재분석 흐름 | `internal/app` | 1~2 | 완료 |
| `exporter.py` | TXT·CSV·JSON 내보내기 | `internal/export` | 2 | 완료 |
| `review.py` | 후보 문맥, 재생 시각 채팅 | `internal/app`, `internal/api` 채팅 조회 | 2 | 완료 |
| `playback.py` | HLS 주소 | `internal/platform/chzzk` `PlaybackSource` | 2 | 완료 |
| `player.js` | 영상 재생·채팅 동기화 | `web/src/features/vod` (npm `hls.js`) | 2 | 완료 |
| `vendor/hls.min.js` | HLS 라이브러리 | npm `hls.js`로 대체 | 2 | 완료 |
| `live_transport.py` | LIVE 정보 조회, 익명 토큰, READ WebSocket | `internal/platform/chzzk` `LiveSource` | 3 | 완료 (실제 생방송 확인 대기) |
| `live_service.py` | LIVE 작업, 큐, 재접속·백오프, 중단·이어 기록 | `internal/app` `live.go` | 3 | 완료 |
| `live_store.py` | LIVE 세션·구간·이벤트·후보·변경 저장, checkpoint | `internal/store` `live.go` (VOD와 통합), `internal/export` | 3 | 완료 |
| `live.js` | LIVE 화면 | `web/src/features/live` | 3 | 완료 |
| `viewers.py` | 웹 시청자 수 보정 | `internal/detect` `viewers.go`, 시청자 수는 `LiveMeta.Viewers` | 3 | 완료 |
| `viewers.py` | 공식 API 시청자 수 | — | 보류 | 보류 |
| `comments.py` | 영상 댓글 수집, 타임스탬프 추출 | `internal/platform/chzzk` `comments.go` (`CommentSource`), `internal/app` `comments.go`·`timestamps.go` | 4 | 완료 (파서 46개 사례 일치, 로그인 댓글 조회는 보류) |
| `timeline.py` | 자동 후보 + 시청자 추천 통합 | `internal/app` `timeline.go`, `internal/export` | 4 | 완료 |
| `v2.js` | 통합 타임라인·댓글·AI 화면 | `web/src/features/session` (`UnifiedList`, `CommentsPanel`, `AIPanel`) | 4 | 완료 (공식 API 시청자 기록 패널은 보류) |
| `history.py` | 저장 목록 숨김·복원 | `internal/store`, `internal/app` | 4 | 완료 |
| `ai.py` | 미리보기, OpenAI 호출, 결과 검증 | `internal/ai`, `internal/ai/openai`, `internal/app` `ai.go` (5단계에 `internal/ai/anthropic` 추가) | 4, 5 | 4단계 완료 (OpenAI) |

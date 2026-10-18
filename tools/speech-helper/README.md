# speech-helper

MomentForge가 macOS의 내장 음성 인식(Speech 프레임워크의 `SpeechAnalyzer`, macOS 26 이상)을 쓰기 위한 작은 실행파일입니다. Apple 프레임워크만 쓰고(외부 의존성 없음), 모델은 macOS가 받아 관리하며, 음성은 이 컴퓨터 밖으로 나가지 않습니다.

`make speech-helper`가 `swiftc`로 빌드해 `cmd/momentforge/assets/speech-helper`에 두고, macOS용 `momentforge` 실행파일이 그 파일을 `embed`해 첫 사용 때 데이터 폴더 `runtime/`에 꺼내 실행합니다. Xcode 명령줄 도구(`xcode-select --install`)가 필요합니다. 출력은 JSON 줄입니다.

```sh
speech-helper locales                 # 지원·설치된 언어, 한국어 여부
speech-helper probe <media>           # 길이(ms), 음성·영상 트랙 유무, 해상도
speech-helper transcribe <audio> [--from 초] [--seconds 초] [--locale ko-KR] [--install]
                                      # 발언마다 {"start_ms","end_ms","text"}, 끝에 {"done":true,…}
```

2026-10-18 측정(Apple M4, macOS 27, NAS의 7시간 44분 m4a): 한국어 지원·설치됨, 5분 → 2.2초, 30분 → 9초(구간 추출 9초 별도), 실시간의 약 140~190배. 발언 단위 시각이 채팅과 맞고, 노래 구간의 영어 가사는 깨짐.

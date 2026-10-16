# 외부 스킬 출처

이 폴더의 스킬 중 아래 4개는 [anthropics/skills](https://github.com/anthropics/skills)에서 그대로 가져온 것입니다. 수정하지 않고, 갱신할 때는 통째로 교체합니다. 라이선스는 각 폴더의 `LICENSE.txt`(Apache-2.0)를 따릅니다.

| 스킬 | 원본 경로 | 커밋 |
|---|---|---|
| frontend-design | `skills/frontend-design` | `dbd4588f9e1033efb41dad4bef2f7947c8993d44` (2026-10-09) |
| webapp-testing | `skills/webapp-testing` | 같음 |
| mcp-builder | `skills/mcp-builder` | 같음 |
| skill-creator | `skills/skill-creator` | 같음 |

claude-api 스킬은 Claude Code에 내장되어 있어 여기에 넣지 않았습니다. 모델과 API가 바뀔 때마다 갱신되므로 내장 버전을 씁니다.

프로젝트 전용 스킬(직접 작성, MIT):

| 스킬 | 역할 |
|---|---|
| momentforge-architecture | 코드 작성·수정·리팩토링 시 자동 적용되는 구조·코드 규칙과 검사 스크립트 |
| port-legacy-module | legacy-python 브랜치의 기존 기능을 새 구조로 옮기는 절차 |

## 갱신 방법

```bash
SHA=<새 커밋>
TMP=$(mktemp -d)
curl -sSL "https://codeload.github.com/anthropics/skills/tar.gz/$SHA" | tar -xz -C "$TMP"
for s in frontend-design webapp-testing mcp-builder skill-creator; do
  rm -rf ".claude/skills/$s" && cp -R "$TMP"/*/skills/$s .claude/skills/
done
```

갱신 후 위 표의 커밋을 바꾸고, 변경 내용이 momentforge-architecture의 규칙과 충돌하지 않는지 확인합니다.

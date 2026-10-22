import type { LiveState, Session } from "../api/types";
import { timecode } from "./format";

export const liveStateText: Record<LiveState, string> = {
  starting: "연결 준비 중",
  collecting: "채팅 수신 중",
  reconnecting: "재연결 중",
  stopping: "남은 자료 저장 중",
  stopped: "중단됨",
  ended: "방송 종료",
  error: "확인 필요",
  incomplete: "저장 상태 확인 필요",
};

export const vodStateText: Partial<Record<Session["state"], string>> = {
  collecting: "수집 중",
  paused: "일시정지",
  finished: "수집 완료",
  comments_only: "댓글만 수집",
};

/** LIVE captures in these states are no longer receiving chat. */
export const liveTerminal = new Set<string>(["stopped", "ended", "error", "incomplete"]);

export function isRunningLive(s: Session): boolean {
  return s.kind === "live" && !liveTerminal.has(s.state);
}

export function sessionHref(s: Session): string {
  return s.kind === "live" ? `/live/${s.id}` : `/sessions/${s.id}`;
}

/**
 * What a saved session is and where it stands: "치지직 다시보기 수집 완료",
 * "치지직 생방송 중단됨", "예시 채팅". The kind is always said, so a VOD and
 * a capture of the same broadcast (same title) tell apart in the list.
 */
export function sessionKindText(s: Session): string {
  if (s.isDemo) return "예시 채팅";
  if (s.kind === "live") return `${s.platformName} 생방송 ${liveStateText[s.state as LiveState] ?? s.state}`;
  return `${s.platformName} 다시보기 ${vodStateText[s.state] ?? s.state}`;
}

/** One line for lists: kind and state, chat count and length. */
export function sessionSummary(s: Session): string {
  const length = timecode(s.durationMs, s.durationMs >= 3_600_000);
  return `${sessionKindText(s)}, 채팅 ${s.chatCount.toLocaleString("ko-KR")}건, 길이 ${length}`;
}

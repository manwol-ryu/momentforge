import { useMutation } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { AIRun } from "../../api/types";
import { ConfirmAction } from "../confirm/ConfirmAction";
import { timecode, tokens, usd } from "../format";

const runStateText: Record<AIRun["state"], string> = {
  running: "보내는 중",
  completed: "완료",
  partial: "일부 완료",
  failed: "실패",
  cancelled: "중단됨",
};

/** What a run does, as its status line names it. */
export const runKindText: Record<AIRun["kind"], string> = {
  explain: "장면 설명",
  cut: "컷 분석",
  transcript: "대본 다듬기",
};

/** The status line of a run: progress, tokens with cache hits, cost. */
export function runSummary(r: AIRun): string {
  const waiting = r.state === "running" && r.delivery === "batch";
  const parts = [
    waiting ? `배치 결과 기다리는 중: ${r.completedRequests}/${r.requestCount}회 처리` : `${r.completedRequests}/${r.requestCount}회 완료`,
  ];
  if (r.kind === "cut") parts.push(`컷 분석 ${r.recommendations}개`);
  else if (r.kind === "explain") parts.push(`설명·추천 ${r.recommendations}개`);
  if (r.rejected > 0) parts.push(`검증에서 뺀 ${r.kind === "transcript" ? "고침" : "추천"} ${r.rejected}개`);
  if (r.inputTokens > 0) {
    const cache = r.cacheReadTokens > 0 ? `(캐시 적중 ${tokens(r.cacheReadTokens)})` : "";
    parts.push(`토큰 입력 ${tokens(r.inputTokens)}${cache}·출력 ${tokens(r.outputTokens)}`);
  }
  if (r.costUsd != null) parts.push(`비용 ${usd(r.costUsd)}`);
  return parts.join(", ");
}

/**
 * One run's progress: what it does, its state, counts, tokens and cost;
 * stopping it (asked once more, since what was sent is paid for); and a cut
 * analysis' search round waiting to be sent or skipped.
 */
export function RunStatus({ r, longForm }: { r: AIRun; longForm: boolean }) {
  const cancel = useMutation({ mutationFn: () => api.aiCancel(r.id) });
  const proceed = useMutation({ mutationFn: (send: boolean) => api.aiContinue(r.id, send) });
  const batch = r.delivery === "batch";
  const failed = cancel.error ?? proceed.error;
  return (
    <>
      <div className="ai-status" role="status" data-state={r.state}>
        <p className="ai-status-line">
          <span className={r.state === "running" ? "shimmer" : undefined}>
            AI {runKindText[r.kind] ?? ""} {r.state === "running" && batch ? "배치" : runStateText[r.state]}
          </span>
          : {runSummary(r)}
          {r.waitingForKey && ". API 키를 다시 저장하면 결과를 가져옵니다"}
          {r.errorMessage && ` (${r.errorMessage})`}
        </p>
        {r.state === "running" && (
          <ConfirmAction
            label={batch ? "배치 취소" : "AI 작업 중단"}
            question={
              batch
                ? "제공자에게 배치 취소를 요청합니다. 이미 처리된 요청의 비용은 들 수 있습니다."
                : "지금까지 받은 결과만 남기고 멈춥니다. 이미 보낸 요청의 비용은 듭니다."
            }
            keepLabel={batch ? "계속 기다리기" : "계속 보내기"}
            onConfirm={() => cancel.mutate()}
            disabled={cancel.isPending}
            className="link-button"
          />
        )}
      </div>
      {r.state === "running" && r.pending && (
        <div className="ai-pending" role="status" aria-live="polite">
          <p className="ai-pending-head">
            검색 결과를 보낼까요? {r.pending.round}단계 검색이 발언 {r.pending.utterances}건·채팅 {r.pending.chats}건(
            {Math.max(1, Math.round(r.pending.bytes / 1024))}KB)을 찾았습니다. 보내면 이 근거로 분석을 다시 하고, 보내지 않으면 처음
            범위만으로 계획합니다.
          </p>
          <ul className="ai-pending-list">
            {r.pending.evidence.map((e) => (
              <li key={e.ref}>
                <span className="timecode">{timecode(e.start_ms, longForm)}</span>{" "}
                <span className="chat-author">{e.kind === "utterance" ? (e.speaker ?? "방송자") : (e.author?.slice(0, 4) ?? "익명")}</span>{" "}
                {e.text}
              </li>
            ))}
          </ul>
          <p className="actions">
            <button type="button" className="button" onClick={() => proceed.mutate(true)} disabled={proceed.isPending}>
              보내고 계속
            </button>
            <button type="button" className="button button-quiet" onClick={() => proceed.mutate(false)} disabled={proceed.isPending}>
              보내지 않고 계속
            </button>
          </p>
        </div>
      )}
      {failed && (
        <p className="error" role="alert">
          {failed.message}
        </p>
      )}
    </>
  );
}

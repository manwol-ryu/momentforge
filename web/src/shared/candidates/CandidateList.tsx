import type { ReactNode } from "react";
import type { Candidate, CandidateState } from "../../api/types";
import { timecode } from "../format";

interface Props {
  candidates: Candidate[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  longForm?: boolean;
  /** Extra controls under a candidate (seek, open on the platform). */
  renderActions?: (c: Candidate) => ReactNode;
  /** Shown under the selected candidate (e.g. its context chat). */
  renderDetail?: (c: Candidate) => ReactNode;
  /** Shown when there are no candidates. */
  emptyText?: string;
}

const stateText: Record<CandidateState, string> = {
  generating: "반응 진행 중",
  waiting_context: "뒤 채팅 기다리는 중",
  confirmed: "확정",
  partial: "부분 문맥으로 마감",
};

/** Candidates in time order with their reasons and a few evidence messages. */
export function CandidateList({ candidates, selectedId, onSelect, longForm = false, renderActions, renderDetail, emptyText }: Props) {
  if (candidates.length === 0) {
    return <p className="empty">{emptyText ?? "반응이 몰린 구간을 찾지 못했습니다. 더 작은 반응까지 보려면 '항상 가장 민감하게'로 다시 분석하세요."}</p>;
  }
  return (
    <ol className="candidates">
      {candidates.map((c, i) => {
        const selected = c.id === selectedId;
        return (
          <li key={c.id} className="candidate" data-selected={selected || undefined}>
            <button type="button" className="candidate-head" aria-pressed={selected} onClick={() => onSelect(c.id)}>
              <span className="candidate-time timecode">
                {timecode(c.reactionStartMs, longForm)}–{timecode(c.reactionEndMs, longForm)}
              </span>
              <span className="candidate-reasons">{c.reasons.join(", ")}</span>
              {c.state && (
                <span className="candidate-state" data-state={c.state}>
                  {stateText[c.state]}
                </span>
              )}
              <span className="visually-hidden">후보 {i + 1}</span>
            </button>
            <p className="candidate-meta">
              가장 활발한 10초에 채팅 {c.peak.n}건, 참여 {c.peak.u}명
              {c.peak.gate ? ` (그때 판정 시작 기준: 채팅 ${c.peak.gate}건 또는 ${c.peak.spikeGate ?? "-"}명)` : ""}. 편집 구간{" "}
              <span className="timecode">
                {timecode(c.clipStartMs, longForm)}–{timecode(c.clipEndMs, longForm)}
              </span>
            </p>
            {!c.baselineReady && <p className="candidate-note">평소 채팅량을 학습하기 전이라 임시 후보입니다.</p>}
            {c.partialContext && <p className="candidate-note">앞뒤 채팅 일부가 없어 문맥이 짧습니다.</p>}
            {renderActions && <div className="candidate-actions">{renderActions(c)}</div>}
            {selected && (
              <div className="evidence">
                <p className="evidence-title">근거 채팅 {c.evidenceTotal}건, 많이 나온 문장 순</p>
                <ul>
                  {c.evidence.map((e, j) => (
                    <li key={j}>
                      <span className="evidence-text">{e.text}</span>{" "}
                      <span className="evidence-count">{e.count}건</span>{" "}
                      <span className="evidence-first">
                        처음 <span className="timecode">{timecode(e.offsetMs, longForm)}</span>
                      </span>
                    </li>
                  ))}
                </ul>
                {renderDetail?.(c)}
              </div>
            )}
          </li>
        );
      })}
    </ol>
  );
}

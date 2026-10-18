import type { CSSProperties, ReactNode } from "react";
import type { Candidate, CandidateState } from "../../api/types";
import { timecode } from "../format";

interface Props {
  candidates: Candidate[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  longForm?: boolean;
  /** Extra controls under a candidate (open on the platform, pick for AI). */
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
      {candidates.map((c, i) => (
        <CandidateItem
          key={c.id}
          c={c}
          index={i}
          selected={c.id === selectedId}
          onSelect={onSelect}
          longForm={longForm}
          renderActions={renderActions}
          renderDetail={renderDetail}
        />
      ))}
    </ol>
  );
}

interface ItemProps {
  c: Candidate;
  index: number;
  /** Position in the shown list, for the staggered entrance (defaults to index). */
  order?: number;
  selected: boolean;
  onSelect: (id: string) => void;
  longForm?: boolean;
  /** Label before the reasons, e.g. the source in a mixed list. */
  badge?: ReactNode;
  /** What the head does besides selecting: "영상에서 보기" when a video plays from it. */
  headHint?: string;
  /** Shown under the summary line whether or not the row is selected (an AI explanation). */
  extra?: ReactNode;
  /** The row's timeline item ID, so the page can bring the chosen row into view. */
  itemId?: string;
  renderActions?: (c: Candidate) => ReactNode;
  renderDetail?: (c: Candidate) => ReactNode;
}

/**
 * One detected candidate: its clip range first (what an editor cuts), the
 * reasons, the peak numbers and, when selected, its evidence.
 */
export function CandidateItem({ c, index, order, selected, onSelect, longForm = false, badge, headHint, extra, itemId, renderActions, renderDetail }: ItemProps) {
  const topCount = Math.max(1, ...c.evidence.map((e) => e.count));
  return (
    <li
      className="candidate"
      data-selected={selected || undefined}
      data-source="rule"
      data-item={itemId}
      style={{ "--i": order ?? index } as CSSProperties}
    >
      <button type="button" className="candidate-head" aria-pressed={selected} title={headHint} onClick={() => onSelect(c.id)}>
        <span className="candidate-time timecode">
          {timecode(c.clipStartMs, longForm)}–{timecode(c.clipEndMs, longForm)}
        </span>
        {badge}
        <span className="candidate-reasons">{c.reasons.join(", ")}</span>
        {c.state && (
          <span className="candidate-state" data-state={c.state}>
            {stateText[c.state]}
          </span>
        )}
        <span className="visually-hidden">후보 {index + 1}</span>
      </button>
      <p className="candidate-meta">
        반응{" "}
        <span className="timecode">
          {timecode(c.reactionStartMs, longForm)}–{timecode(c.reactionEndMs, longForm)}
        </span>
        , 가장 활발한 10초에 채팅 {c.peak.n}건·참여 {c.peak.u}명
      </p>
      {!c.baselineReady && <p className="candidate-note">평소 채팅량을 학습하기 전이라 임시 후보입니다.</p>}
      {c.partialContext && <p className="candidate-note">앞뒤 채팅 일부가 없어 문맥이 짧습니다.</p>}
      {extra}
      {renderActions && <div className="candidate-actions">{renderActions(c)}</div>}
      {selected && (
        <div className="evidence">
          <p className="evidence-title">근거 채팅 {c.evidenceTotal}건, 많이 나온 문장 순</p>
          <ul className="bar-list">
            {c.evidence.map((e, j) => (
              <li key={j} className="bar-row">
                {/* Behind each line, a bar as long as its share of the most repeated one. */}
                <span className="bar-fill" style={{ "--p": e.count / topCount } as CSSProperties} aria-hidden="true" />
                <span className="evidence-text">{e.text}</span> <span className="evidence-count">{e.count}건</span>{" "}
                <span className="evidence-first">
                  처음 <span className="timecode">{timecode(e.offsetMs, longForm)}</span>
                </span>
              </li>
            ))}
          </ul>
          {c.peak.gate ? (
            <p className="candidate-note">
              그때 판정 시작 기준: 10초에 채팅 {c.peak.gate}건 또는 채팅한 사람 {c.peak.spikeGate ?? "-"}명.
            </p>
          ) : null}
          {renderDetail?.(c)}
        </div>
      )}
    </li>
  );
}

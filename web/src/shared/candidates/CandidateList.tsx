import type { CSSProperties, ReactNode } from "react";
import type { Candidate, CandidateState } from "../../api/types";
import { timecode } from "../format";
import { measureParts, strengthLabel, topMeasures } from "./strength";

const stateText: Record<CandidateState, string> = {
  generating: "반응 진행 중",
  waiting_context: "뒤 채팅 기다리는 중",
  confirmed: "확정",
  partial: "부분 문맥으로 마감",
};

const usualFormat = new Intl.NumberFormat("ko-KR", { maximumFractionDigits: 1 });

interface ItemProps {
  c: Candidate;
  /** The candidate's number, as its timeline marker shows it ("후보 3"). */
  number: number;
  /** Position in the shown list, for the staggered entrance. */
  order: number;
  selected: boolean;
  onSelect: (id: string) => void;
  longForm?: boolean;
  /** Label before the reasons, e.g. the source in a mixed list. */
  badge?: ReactNode;
  /** What the head does besides selecting: "영상에서 보기" when a video plays from it. */
  headHint?: string;
  /** Shown under the summary line whether or not the row is selected (AI results, a line each until it is chosen). */
  extra?: ReactNode;
  /** The row's timeline item ID, so the page can bring the chosen row into view. */
  itemId?: string;
  /** The window score a candidate's peak needs in its analysis, for the "why" line of a chosen candidate. */
  scoreThreshold?: number;
  renderActions?: (c: Candidate) => ReactNode;
  renderDetail?: (c: Candidate) => ReactNode;
}

/**
 * One detected candidate: its number and clip range first (what an editor
 * cuts), how strong it is for this broadcast, the reasons, what stood out
 * at the peak against the usual for that time and, when selected, its
 * evidence.
 */
export function CandidateItem({
  c,
  number,
  order,
  selected,
  onSelect,
  longForm = false,
  badge,
  headHint,
  extra,
  itemId,
  scoreThreshold,
  renderActions,
  renderDetail,
}: ItemProps) {
  const topCount = Math.max(1, ...c.evidence.map((e) => e.count));
  const measures = topMeasures(c.measures);
  const seconds = (measures[0]?.windowMs ?? 10_000) / 1000;
  const scoreFacts = [
    scoreThreshold !== undefined ? `후보 기준 ${usualFormat.format(scoreThreshold)}` : "",
    c.rank !== undefined ? `강한 순 ${c.rank}위` : "",
  ]
    .filter(Boolean)
    .join(", ");
  return (
    <li
      className="candidate"
      data-selected={selected || undefined}
      data-source="rule"
      data-strength={c.strength}
      data-item={itemId}
      style={{ "--i": order } as CSSProperties}
    >
      <button type="button" className="candidate-head" aria-pressed={selected} title={headHint} onClick={() => onSelect(c.id)}>
        <span className="candidate-line">
          <span className="candidate-no">후보 {number}</span>
          <span className="candidate-time timecode">
            {timecode(c.clipStartMs, longForm)}–{timecode(c.clipEndMs, longForm)}
          </span>
          {c.strength && (
            <span className="candidate-strength" data-strength={c.strength}>
              {strengthLabel[c.strength]}
            </span>
          )}
          {c.rank !== undefined && <span className="candidate-rank">{c.rank}위</span>}
        </span>
        <span className="candidate-line">
          {badge}
          <span className="candidate-reasons">{c.reasons.join(", ")}</span>
          {c.state && (
            <span className="candidate-state" data-state={c.state}>
              {stateText[c.state]}
            </span>
          )}
        </span>
      </button>
      <p className="candidate-meta">
        반응{" "}
        <span className="timecode">
          {timecode(c.reactionStartMs, longForm)}–{timecode(c.reactionEndMs, longForm)}
        </span>
        {measures.length > 0 ? (
          <>
            , {seconds}초에{" "}
            {measures.map((m, i) => {
              const p = measureParts(m);
              return (
                <span key={`${m.key}-${i}`} className="measure">
                  {i > 0 && ", "}
                  <strong>{p.observed}</strong> ({p.usual})
                </span>
              );
            })}
          </>
        ) : (
          <>
            , 가장 활발한 10초에 채팅 {c.peak.n}건·참여 {c.peak.u}명
          </>
        )}
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
          {c.score !== undefined ? (
            <p className="candidate-note">
              평소 대비 점수 {usualFormat.format(c.score)}
              {scoreFacts && ` (${scoreFacts})`}. 같은 방송의 앞뒤 몇 분과 전체 흐름에 비춰 얼마나 튀었는지 잰 점수입니다.
            </p>
          ) : c.peak.gate ? (
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

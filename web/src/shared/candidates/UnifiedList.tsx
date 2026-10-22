import type { CSSProperties, ReactNode } from "react";
import type { Candidate, CutBoundaryName, TimelineItem, TimelineSource, TimelineSpan } from "../../api/types";
import { CandidateItem } from "./CandidateList";
import { boundaryLabel, CutBox, startBoundary } from "./CutBox";
import { timecode } from "../format";

export const sourceLabel: Record<TimelineSource, string> = {
  rule: "자동 탐지",
  viewer_comment: "시청자 추천",
  ai: "AI 추천",
};

export const priorityLabel = { high: "높음", medium: "보통", low: "낮음" } as const;

interface Props {
  items: TimelineItem[];
  /** Rule candidates by their ID, for the detailed rule rows. */
  candidates: Map<string, Candidate>;
  /** AI explanations under the rows they belong to (foldAI). */
  aiFor?: Map<string, TimelineItem[]>;
  selectedId: string | null;
  /** Select a row; with a video the row's clip also plays from its start. */
  onSelect: (itemId: string) => void;
  longForm: boolean;
  canPlay: boolean;
  platformName: string;
  /** Play from a time (an AI explanation's own range). */
  onSeek?: (ms: number, itemId: string) => void;
  /** Play a cut plan's spans one after another. */
  onPlaySpans?: (spans: TimelineSpan[], itemId: string) => void;
  /** The boundary candidate shown per cut plan ID (absent: the confirmed one, else the analysis' choice). */
  cutChoice?: Record<string, CutBoundaryName>;
  onCutChoice?: (planId: string, name: CutBoundaryName, itemId: string) => void;
  /** Ask for a new cut analysis of a candidate (its notes go along); label is how the list names the row. */
  onReanalyzeCut?: (itemId: string, label: string, planId?: string) => void;
  /** Timeline item IDs ticked for an AI review. */
  aiSelection: Set<string>;
  onToggleAI: (itemId: string) => void;
  /** Whether a non-AI item can be ticked now (default: always); a LIVE candidate waits until it is settled. */
  canPick?: (it: TimelineItem) => boolean;
  /** Rule candidate numbers by candidate ID, as the timeline marks them (candidateNumbers). */
  numbers: Map<string, number>;
  /** The window score a candidate's peak needs in this analysis (whole-broadcast analyses). */
  scoreThreshold?: number;
  renderRuleDetail: (c: Candidate) => ReactNode;
}

/**
 * Every source's moments (CzzEdit's 통합 타임라인), in the order given.
 * Each row says where it came from; comment and AI text is shown as text,
 * and an AI result about a listed candidate sits under that candidate: a
 * line each until the row is chosen, then in full.
 */
export function UnifiedList({
  items,
  candidates,
  aiFor,
  selectedId,
  onSelect,
  longForm,
  canPlay,
  platformName,
  onSeek,
  onPlaySpans,
  cutChoice,
  onCutChoice,
  onReanalyzeCut,
  aiSelection,
  onToggleAI,
  canPick,
  numbers,
  scoreThreshold,
  renderRuleDetail,
}: Props) {
  const headHint = canPlay ? "고르면 영상이 이 구간부터 재생됩니다" : undefined;
  // Each row's checkbox says which row it is, so a screen reader's list of them is not "AI 검토에 포함" over and over.
  const rowName = (it: TimelineItem) =>
    it.source === "rule" && numbers.has(it.originId)
      ? `후보 ${numbers.get(it.originId)}`
      : `${sourceLabel[it.source]} ${timecode(it.clipStartMs, longForm)}`;
  const actions = (it: TimelineItem) => (
    <>
      {it.watchUrl && (
        <a href={it.watchUrl} target="_blank" rel="noreferrer">
          {platformName}에서 열기
        </a>
      )}
      {it.source !== "ai" &&
        (canPick?.(it) ?? true ? (
          <label className="check check-inline">
            <input type="checkbox" checked={aiSelection.has(it.id)} onChange={() => onToggleAI(it.id)} />
            <span className="visually-hidden">{rowName(it)} </span>AI 검토에 포함
          </label>
        ) : (
          <label className="check check-inline" data-disabled>
            <input type="checkbox" checked={false} disabled />
            <span className="visually-hidden">{rowName(it)} </span>AI 검토에 포함 (확정 후)
          </label>
        ))}
    </>
  );
  const explanations = (it: TimelineItem, selected: boolean) => renderNotes(it, aiFor?.get(it.id), selected);
  // AI results shown with a row: one line each until the row is chosen. A
  // cut plan whose moment has no candidate now is its own row and host.
  const renderNotes = (it: TimelineItem, notes: TimelineItem[] | undefined, selected: boolean) => {
    if (!notes?.length) return null;
    // A long list stays a list: the full results open with their row.
    if (!selected) {
      return (
        <div className="ai-notes">
          {notes.map((n) => (
            <AIBrief key={n.id} it={n} onOpen={() => onSelect(it.id)} />
          ))}
        </div>
      );
    }
    return (
      <div className="ai-notes">
        {notes.map((n) =>
          n.kind === "cut" ? (
            <CutBox
              key={n.id}
              it={n}
              longForm={longForm}
              choice={(n.cut && (cutChoice?.[n.cut.planId] ?? startBoundary(n.cut))) || "context"}
              onChoose={n.cut && onCutChoice ? (name) => onCutChoice(n.cut!.planId, name, it.id) : undefined}
              onPlay={onSeek ? (ms) => onSeek(ms, it.id) : undefined}
              onPlaySpans={canPlay && onPlaySpans ? (spans) => onPlaySpans(spans, it.id) : undefined}
              onReanalyze={onReanalyzeCut && it.source !== "ai" ? () => onReanalyzeCut(it.id, rowName(it), n.cutPlanId) : undefined}
            />
          ) : (
            <AINote key={n.id} it={n} longForm={longForm} onPlay={canPlay && onSeek ? () => onSeek(n.clipStartMs, it.id) : undefined} />
          ),
        )}
      </div>
    );
  };
  return (
    <ol className="candidates">
      {items.map((it, pos) => {
        const selected = it.id === selectedId;
        const rule = it.source === "rule" ? candidates.get(it.originId) : undefined;
        if (rule) {
          return (
            <CandidateItem
              key={it.id}
              c={rule}
              number={numbers.get(rule.id) ?? pos + 1}
              order={pos}
              selected={selected}
              onSelect={() => onSelect(it.id)}
              longForm={longForm}
              headHint={headHint}
              itemId={it.id}
              scoreThreshold={scoreThreshold}
              badge={<span className="candidate-source" data-source="rule">자동 탐지</span>}
              extra={explanations(it, selected)}
              renderActions={() => actions(it)}
              renderDetail={renderRuleDetail}
            />
          );
        }
        return (
          <li
            key={it.id}
            className="candidate"
            data-source={it.source}
            data-selected={selected || undefined}
            data-item={it.id}
            style={{ "--i": pos } as CSSProperties}
          >
            <button type="button" className="candidate-head" aria-pressed={selected} title={headHint} onClick={() => onSelect(it.id)}>
              <span className="candidate-time timecode">
                {timecode(it.clipStartMs, longForm)}–{timecode(it.clipEndMs, longForm)}
              </span>
              <span className="candidate-source" data-source={it.source}>
                {it.kind === "cut" ? "AI 컷 분석" : sourceLabel[it.source]}
              </span>
              <span className="candidate-reasons">{it.kind === "cut" ? "지금 분석에는 이 장면의 후보가 없습니다" : it.title}</span>
              {it.priority && <span className="candidate-state">중요도 {priorityLabel[it.priority]}</span>}
            </button>
            {it.commentText && <p className="candidate-meta source-note">영상 댓글: {it.commentText}</p>}
            {it.kind === "cut" ? (
              renderNotes(it, [it], selected)
            ) : (
              <>
                {it.rationale && <p className="candidate-meta candidate-text">{it.rationale}</p>}
                {it.uncertainty && <p className="candidate-note">판단 한계: {it.uncertainty}</p>}
              </>
            )}
            {explanations(it, selected)}
            <div className="candidate-actions">{actions(it)}</div>
          </li>
        );
      })}
    </ol>
  );
}

/**
 * An AI result in one line under a row that is not chosen: the kind and
 * version, whether the plan passed its checks and is confirmed, and what it
 * found. Pressing it chooses the row, which opens the result in full.
 */
function AIBrief({ it, onOpen }: { it: TimelineItem; onOpen: () => void }) {
  if (it.kind === "cut") {
    const c = it.cut;
    const passed = c ? c.state === "ok" : !!it.verified;
    const version = c?.version ?? it.version;
    return (
      <button type="button" className="ai-brief" title="후보를 고르면 컷 분석 전체가 펼쳐집니다" onClick={onOpen}>
        <span className="candidate-source" data-source="ai">
          AI 컷 분석{version ? ` v${version}` : ""}
        </span>
        <span className="cut-state" data-ok={passed || undefined}>
          {passed ? "검수 통과" : "검수 실패"}
        </span>
        {c?.confirmed && <span className="cut-confirmed">확정 · {boundaryLabel[startBoundary(c)]}</span>}
        <span className="ai-brief-text">{c?.target || c?.causes[0]?.summary || it.title}</span>
      </button>
    );
  }
  return (
    <button type="button" className="ai-brief" title="후보를 고르면 AI 설명 전체가 펼쳐집니다" onClick={onOpen}>
      <span className="candidate-source" data-source="ai">
        AI 설명
      </span>
      {it.priority && <span className="ai-note-priority">중요도 {priorityLabel[it.priority]}</span>}
      <span className="ai-brief-text">{it.title || it.rationale}</span>
    </button>
  );
}

/** What the AI made of a candidate: the scene it guessed, why, and its own range. */
function AINote({ it, longForm, onPlay }: { it: TimelineItem; longForm: boolean; onPlay?: () => void }) {
  return (
    <div className="ai-note" data-priority={it.priority}>
      <p className="ai-note-head">
        <span className="candidate-source" data-source="ai">
          AI 설명
        </span>
        {it.priority && <span className="ai-note-priority">중요도 {priorityLabel[it.priority]}</span>}
        <strong className="ai-note-title">{it.title}</strong>
      </p>
      {it.rationale && <p className="ai-note-text">{it.rationale}</p>}
      <p className="ai-note-foot">
        <span>
          AI가 잡은 구간{" "}
          <span className="timecode">
            {timecode(it.clipStartMs, longForm)}–{timecode(it.clipEndMs, longForm)}
          </span>
        </span>
        {onPlay && (
          <button type="button" className="link-button" onClick={onPlay}>
            이 구간 재생
          </button>
        )}
      </p>
      {it.uncertainty && (
        <details className="ai-note-more">
          <summary>판단 한계</summary>
          <p className="candidate-note">{it.uncertainty}</p>
        </details>
      )}
    </div>
  );
}

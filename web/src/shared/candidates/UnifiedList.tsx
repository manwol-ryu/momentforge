import type { CSSProperties, ReactNode } from "react";
import type { Candidate, TimelineItem, TimelineSource, TimelineSpan } from "../../api/types";
import { CandidateItem } from "./CandidateList";
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
  /** Timeline item IDs ticked for an AI review. */
  aiSelection: Set<string>;
  onToggleAI: (itemId: string) => void;
  /** Whether a non-AI item can be ticked now (default: always); a LIVE candidate waits until it is settled. */
  canPick?: (it: TimelineItem) => boolean;
  renderRuleDetail: (c: Candidate) => ReactNode;
}

/**
 * Every source's moments in video time order (CzzEdit's 통합 타임라인).
 * Each row says where it came from; comment and AI text is shown as text,
 * and an AI explanation of a listed candidate sits under that candidate.
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
  aiSelection,
  onToggleAI,
  canPick,
  renderRuleDetail,
}: Props) {
  const headHint = canPlay ? "고르면 영상이 이 구간부터 재생됩니다" : undefined;
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
            AI 검토에 포함
          </label>
        ) : (
          <label className="check check-inline" data-disabled>
            <input type="checkbox" checked={false} disabled />
            AI 검토에 포함 (확정 후)
          </label>
        ))}
    </>
  );
  const explanations = (it: TimelineItem) => {
    const notes = aiFor?.get(it.id);
    if (!notes?.length) return null;
    return (
      <div className="ai-notes">
        {notes.map((n) =>
          n.kind === "cut" ? (
            <CutNote
              key={n.id}
              it={n}
              longForm={longForm}
              onPlay={canPlay && onSeek ? (ms) => onSeek(ms, it.id) : undefined}
              onPlayAll={canPlay && onPlaySpans && n.spans ? () => onPlaySpans(n.spans!, it.id) : undefined}
            />
          ) : (
            <AINote key={n.id} it={n} longForm={longForm} onPlay={canPlay && onSeek ? () => onSeek(n.clipStartMs, it.id) : undefined} />
          ),
        )}
      </div>
    );
  };
  let ruleIndex = 0;
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
              index={ruleIndex++}
              order={pos}
              selected={selected}
              onSelect={() => onSelect(it.id)}
              longForm={longForm}
              headHint={headHint}
              itemId={it.id}
              badge={<span className="candidate-source" data-source="rule">자동 탐지</span>}
              extra={explanations(it)}
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
                {sourceLabel[it.source]}
              </span>
              <span className="candidate-reasons">{it.title}</span>
              {it.priority && <span className="candidate-state">중요도 {priorityLabel[it.priority]}</span>}
            </button>
            {it.commentText && <p className="candidate-meta source-note">영상 댓글: {it.commentText}</p>}
            {it.rationale && <p className="candidate-meta candidate-text">{it.rationale}</p>}
            {it.uncertainty && <p className="candidate-note">판단 한계: {it.uncertainty}</p>}
            {explanations(it)}
            <div className="candidate-actions">{actions(it)}</div>
          </li>
        );
      })}
    </ol>
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

const roleLabel = { setup: "앞선 맥락", main: "본 장면", payoff: "마무리" } as const;

/**
 * A cut plan under its candidate (phase 6-A: the spans, the cause, whether
 * both checks passed; the full result box comes in 6-D).
 */
function CutNote({ it, longForm, onPlay, onPlayAll }: { it: TimelineItem; longForm: boolean; onPlay?: (ms: number) => void; onPlayAll?: () => void }) {
  const spans = it.spans ?? [];
  return (
    <div className="ai-note cut-note" data-verified={it.verified || undefined}>
      <p className="ai-note-head">
        <span className="candidate-source" data-source="ai">
          AI 컷 분석
        </span>
        {it.version && it.version > 1 && <span className="ai-note-priority">v{it.version}</span>}
        <span className="cut-state" data-ok={it.verified || undefined}>
          {it.verified ? "검수 통과" : "검수 실패"}
        </span>
        <strong className="ai-note-title">{it.title}</strong>
      </p>
      {it.rationale && <p className="ai-note-text">원인: {it.rationale}</p>}
      {(it.searchRounds ?? 0) > 0 && (
        <p className="ai-note-text cut-search">
          검색 {it.searchRounds}단계로 근거 {it.searchAdded ?? 0}건을 더 읽음
          {it.storyOriginMs !== undefined && (
            <>
              {" · "}이야기 시작{" "}
              {onPlay ? (
                <button type="button" className="link-button timecode" onClick={() => onPlay(it.storyOriginMs!)}>
                  {timecode(it.storyOriginMs, longForm)}
                </button>
              ) : (
                <span className="timecode">{timecode(it.storyOriginMs, longForm)}</span>
              )}
            </>
          )}
        </p>
      )}
      <ol className="cut-spans">
        {spans.map((sp, i) => (
          <li key={i}>
            <span className="cut-role">{roleLabel[sp.role] ?? sp.role}</span>{" "}
            <span className="timecode">
              {timecode(sp.startMs, longForm)}–{timecode(sp.endMs, longForm)}
            </span>
            {onPlay && (
              <>
                {" "}
                <button type="button" className="link-button" onClick={() => onPlay(sp.startMs)}>
                  재생
                </button>
              </>
            )}
          </li>
        ))}
      </ol>
      <p className="ai-note-foot">
        <span>
          구간 {spans.length}개, 합계{" "}
          <span className="timecode">{timecode(spans.reduce((t, sp) => t + sp.endMs - sp.startMs, 0))}</span>
        </span>
        {onPlayAll && spans.length > 1 && (
          <button type="button" className="link-button" onClick={onPlayAll}>
            구간 이어 재생
          </button>
        )}
      </p>
      {it.uncertainty && (
        <p className={it.verified ? "candidate-note" : "cut-issues"}>
          {it.verified ? "미확정: " : "검수에서 걸린 이유: "}
          {it.uncertainty}
        </p>
      )}
    </div>
  );
}

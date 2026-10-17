import type { CSSProperties, ReactNode } from "react";
import type { Candidate, TimelineItem, TimelineSource } from "../../api/types";
import { CandidateItem } from "./CandidateList";
import { timecode } from "../format";

export const sourceLabel: Record<TimelineSource, string> = {
  rule: "자동 탐지",
  viewer_comment: "시청자 추천",
  ai: "AI 추천",
};

const priorityLabel = { high: "높음", medium: "보통", low: "낮음" } as const;

interface Props {
  items: TimelineItem[];
  /** Rule candidates by their ID, for the detailed rule rows. */
  candidates: Map<string, Candidate>;
  selectedId: string | null;
  onSelect: (itemId: string) => void;
  longForm: boolean;
  canPlay: boolean;
  platformName: string;
  onSeek?: (ms: number, itemId: string) => void;
  /** Timeline item IDs ticked for an AI review. */
  aiSelection: Set<string>;
  onToggleAI: (itemId: string) => void;
  /** Whether a non-AI item can be ticked now (default: always); a LIVE candidate waits until it is settled. */
  canPick?: (it: TimelineItem) => boolean;
  renderRuleDetail: (c: Candidate) => ReactNode;
}

/**
 * Every source's moments in video time order (CzzEdit's 통합 타임라인).
 * Each row says where it came from; comment and AI text is shown as text.
 */
export function UnifiedList({
  items,
  candidates,
  selectedId,
  onSelect,
  longForm,
  canPlay,
  platformName,
  onSeek,
  aiSelection,
  onToggleAI,
  canPick,
  renderRuleDetail,
}: Props) {
  const actions = (it: TimelineItem) => (
    <>
      {canPlay && onSeek && (
        <button
          type="button"
          className="link-button"
          onClick={() => onSeek(it.clipStartMs, it.id)}
          aria-label={`${timecode(it.clipStartMs, longForm)}부터 영상에서 보기`}
        >
          영상에서 보기
        </button>
      )}
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
              badge={<span className="candidate-source" data-source="rule">자동 탐지</span>}
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
            style={{ "--i": pos } as CSSProperties}
          >
            <button type="button" className="candidate-head" aria-pressed={selected} onClick={() => onSelect(it.id)}>
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
            {it.rationale && <p className="candidate-meta">{it.rationale}</p>}
            {it.uncertainty && <p className="candidate-note">판단 한계: {it.uncertainty}</p>}
            <div className="candidate-actions">{actions(it)}</div>
          </li>
        );
      })}
    </ol>
  );
}

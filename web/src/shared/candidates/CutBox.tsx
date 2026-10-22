import { useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { CutBoundaryName, CutDetail, CutEvidence, TimelineItem, TimelineSpan } from "../../api/types";
import { timecode } from "../format";
import { Segmented } from "../motion/Segmented";

export const boundaryLabel: Record<CutBoundaryName, string> = { short: "짧게", context: "맥락", full_event: "전체" };

const roleLabel: Record<TimelineSpan["role"], string> = { setup: "앞선 맥락", main: "본 장면", payoff: "마무리" };

const kindLabel: Record<CutEvidence["kind"], string> = { utterance: "발언", chat: "채팅", comment: "댓글" };

const statusLabel = { confirmed: "확인됨", likely: "유력", undetermined: "미정", "": "" } as const;

const endingLabel = {
  confirmed: "결말 확인",
  pending: "결말 대기",
  cut_off: "읽은 범위에서 결말을 못 봄",
  insufficient: "자료 부족",
  unresolved: "결말 없이 끝난 사건",
  "": "",
} as const;

const answerLabel = [
  ["what", "무엇을"],
  ["condition", "조건"],
  ["result", "결과"],
  ["references", "'아까'가 가리키는 것"],
  ["why_reaction", "왜 반응"],
] as const;

const sourceLabel: Record<string, string> = { utterances: "발언", chat: "채팅", comment: "댓글" };

/** The boundary a plan shows first: the confirmed one, else the analysis' choice. */
export function startBoundary(c: CutDetail): CutBoundaryName {
  return c.confirmedBoundary ?? c.chosen;
}

/** The spans of one boundary candidate (the item's own spans when the plan predates them). */
export function boundarySpans(it: TimelineItem, name: CutBoundaryName): TimelineSpan[] {
  return it.cut?.boundaries.find((b) => b.name === name)?.spans ?? it.spans ?? [];
}

interface Props {
  it: TimelineItem;
  longForm: boolean;
  /** The boundary candidate shown (short | context | full_event). */
  choice: CutBoundaryName;
  onChoose?: (name: CutBoundaryName) => void;
  /** Play from a time: an evidence button or a span. */
  onPlay?: (ms: number) => void;
  /** Play spans one after another. */
  onPlaySpans?: (spans: TimelineSpan[]) => void;
  /** The way to analyse this candidate again (it plans with the notes): a "다시 분석" button. */
  reanalyze?: ReactNode;
}

/**
 * A cut plan under its candidate (docs/AI-CUT-ANALYSIS.md 6.3): what the
 * viewers reacted to and why, with every piece of evidence as a button that
 * plays it; the story's origin, the event's start and the context the clip
 * must keep; the three boundary candidates to switch between; how the event
 * ends and what the checks answered; and the user's notes and confirmation.
 */
// Unsaved notes by plan. A row that is not chosen shows its plan in one
// line, so the box unmounts; a draft waits here until the row opens again.
const drafts = new Map<string, { text: string; open: boolean }>();

export function CutBox({ it, longForm, choice, onChoose, onPlay, onPlaySpans, reanalyze }: Props) {
  const c = it.cut;
  const queryClient = useQueryClient();
  const draftKey = c?.planId ?? it.id;
  const [noteText, setNoteTextState] = useState(() => drafts.get(draftKey)?.text ?? "");
  const [noteOpen, setNoteOpenState] = useState(() => drafts.get(draftKey)?.open ?? false);
  const keepDraft = (text: string, open: boolean) => {
    if (text || open) drafts.set(draftKey, { text, open });
    else drafts.delete(draftKey);
  };
  const setNoteText = (text: string) => {
    setNoteTextState(text);
    keepDraft(text, noteOpen);
  };
  const setNoteOpen = (open: boolean | ((o: boolean) => boolean)) => {
    const next = typeof open === "function" ? open(noteOpen) : open;
    setNoteOpenState(next);
    keepDraft(noteText, next);
  };
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["timeline"] });
    void queryClient.invalidateQueries({ queryKey: ["files"] });
  };
  const confirm = useMutation({
    mutationFn: (on: boolean) => api.confirmCut(c!.planId, on, on ? choice : undefined),
    onSuccess: refresh,
  });
  const note = useMutation({
    mutationFn: () => api.addCutNote(c!.planId, noteText),
    onSuccess: () => {
      setNoteText("");
      setNoteOpen(false);
      refresh();
    },
  });
  const spans = boundarySpans(it, choice);
  const total = spans.reduce((t, sp) => t + sp.endMs - sp.startMs, 0);
  const at = (ms: number) => timecode(ms, longForm);
  const ref = (e: CutEvidence, label?: string) => {
    const text = `${kindLabel[e.kind]}${e.text ? ` "${e.text}"` : ""}`;
    if (e.atMs === undefined) {
      return (
        <span className="cut-ref" title={text} key={e.ref}>
          {label ?? kindLabel[e.kind]}
        </span>
      );
    }
    const inner = (
      <>
        {onPlay && <span aria-hidden="true">▶ </span>}
        <span className="timecode">{at(e.atMs)}</span> {label ?? kindLabel[e.kind]}
      </>
    );
    return onPlay ? (
      <button type="button" className="cut-ref" key={e.ref} title={text} aria-label={`${at(e.atMs)} ${text} 재생`} onClick={() => onPlay(e.atMs!)}>
        {inner}
      </button>
    ) : (
      <span className="cut-ref" key={e.ref} title={text}>
        {inner}
      </span>
    );
  };
  const refs = (list: CutEvidence[]) => (list.length ? list.map((e) => ref(e)) : <span className="cut-none">없음</span>);
  const quote = (e?: CutEvidence) => (e?.text ? <span className="cut-quote">"{e.text.length > 40 ? e.text.slice(0, 40) + "…" : e.text}"</span> : null);

  if (!c) return null;
  const usable = c.boundaries.filter((b) => b.spans.length > 0);
  const [main, ...alternatives] = c.causes;
  const v = c.verification;
  const failed = c.state !== "ok";
  const issues = v.issues.length ? v.issues : [];
  return (
    <div className="ai-note cut-note" data-verified={it.verified || undefined} data-confirmed={c.confirmed || undefined} data-plan={c.planId}>
      <p className="ai-note-head">
        <span className="candidate-source" data-source="ai">
          AI 컷 분석
        </span>
        <span className="ai-note-priority">v{c.version}</span>
        <span className="cut-state" data-ok={!failed || undefined}>
          {failed ? "검수 실패" : "검수 통과"}
        </span>
        {c.confirmed && <span className="cut-confirmed">확정 · {boundaryLabel[startBoundary(c)]}</span>}
        {c.keptVersion && <span className="cut-kept">확정본 v{c.keptVersion} 유지</span>}
        <span className="cut-sources">자료: {c.sources.map((s) => sourceLabel[s] ?? s).join("·")}</span>
        <strong className="ai-note-title">{c.target}</strong>
      </p>

      <dl className="cut-facts">
        {main && (
          <>
            <dt>원인{c.causeStatus && <span className="cut-status"> {statusLabel[c.causeStatus]}</span>}</dt>
            <dd>
              <p className="cut-cause">{main.summary}</p>
              <p className="cut-refs">
                <span className="cut-refs-label">근거</span> {refs(main.support)}
                {main.against.length > 0 && (
                  <>
                    {" "}
                    <span className="cut-refs-label">반대</span> {refs(main.against)}
                  </>
                )}
              </p>
              {alternatives.length > 0 && (
                <ul className="cut-alternatives" aria-label="다른 원인 후보">
                  {alternatives.map((alt, i) => (
                    <li key={i}>
                      <span className="cut-refs-label">대안</span> {alt.summary}{" "}
                      {alt.support.length > 0 && (
                        <>
                          <span className="cut-refs-label">근거</span> {refs(alt.support)}
                        </>
                      )}{" "}
                      {alt.against.length > 0 && (
                        <>
                          <span className="cut-refs-label">반대</span> {refs(alt.against)}
                        </>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </dd>
          </>
        )}
        {(c.origin || c.eventStart) && (
          <>
            <dt>이야기</dt>
            <dd className="cut-refs">
              {c.origin && (
                <span className="cut-story">
                  <span className="cut-refs-label">기원</span> {ref(c.origin, "")} {quote(c.origin)}
                </span>
              )}
              {c.eventStart && (
                <span className="cut-story">
                  <span className="cut-refs-label">사건 시작</span> {ref(c.eventStart, "")}
                </span>
              )}
            </dd>
          </>
        )}
        {c.required.length > 0 && (
          <>
            <dt>필수 맥락</dt>
            <dd className="cut-refs">{refs(c.required)}</dd>
          </>
        )}
        {(c.searchRounds ?? 0) > 0 && (
          <>
            <dt>검색</dt>
            <dd className="cut-search">
              검색 {c.searchRounds}단계로 근거 {c.searchAdded}건을 더 읽음
              {it.storyOriginMs !== undefined && (
                <>
                  {" · "}이야기 시작{" "}
                  {onPlay ? (
                    <button type="button" className="link-button timecode" onClick={() => onPlay(it.storyOriginMs!)}>
                      {at(it.storyOriginMs)}
                    </button>
                  ) : (
                    <span className="timecode">{at(it.storyOriginMs)}</span>
                  )}
                </>
              )}
            </dd>
          </>
        )}
        <dt>구간</dt>
        <dd>
          {usable.length > 1 && onChoose && (
            <Segmented
              name={`cut-boundary-${c.planId}`}
              legend="경계 후보"
              hideLegend
              value={choice}
              onChange={onChoose}
              options={usable.map((b) => ({ value: b.name, label: boundaryLabel[b.name] }))}
            />
          )}
          <ol className="cut-spans">
            {spans.map((sp, i) => (
              <li key={i}>
                <span className="cut-role">{roleLabel[sp.role] ?? sp.role}</span>{" "}
                <span className="timecode">
                  {at(sp.startMs)}–{at(sp.endMs)}
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
          <p className="cut-span-foot">
            <span>
              {boundaryLabel[choice]}
              {choice === c.chosen ? " (AI 선택)" : ""} · 구간 {spans.length}개, 합계 <span className="timecode">{timecode(total)}</span>
            </span>
            {onPlaySpans && spans.length > 1 && (
              <button type="button" className="link-button" onClick={() => onPlaySpans(spans)}>
                구간 이어 재생
              </button>
            )}
          </p>
        </dd>
        <dt>종료</dt>
        <dd>
          {c.endReason}
          {c.ending.state && <span className="cut-ending"> · {endingLabel[c.ending.state]}</span>}
          {c.startReason && <p className="cut-subtle">시작: {c.startReason}</p>}
        </dd>
        <dt>검수</dt>
        <dd>
          <span className="cut-check" data-ok={v.clipPass || undefined}>
            클립만 보기 {v.clipPass === undefined ? "안 함" : v.clipPass ? "통과" : "실패"}
          </span>
          {" · "}
          <span className="cut-check" data-ok={v.sourcePass || undefined}>
            원본 비교 {v.sourcePass === undefined ? "안 함" : v.sourcePass ? "통과" : `실패(${v.findings.join(", ")})`}
          </span>
          {v.answers && (
            <details className="ai-note-more">
              <summary>검수 답 보기{v.serverChanges.length > 0 && ` (서버가 바꾼 분류 ${v.serverChanges.length}건)`}</summary>
              <dl className="cut-answers">
                {answerLabel.map(([k, label]) =>
                  v.answers?.[k] ? (
                    <div key={k}>
                      <dt>{label}</dt>
                      <dd>{v.answers[k]}</dd>
                    </div>
                  ) : null,
                )}
                {v.asides.length > 0 && (
                  <div>
                    <dt>참고 (실패 사유 아님)</dt>
                    <dd>
                      <ul className="cut-findings">
                        {v.asides.map((f, i) => (
                          <li key={i}>
                            {f.text} {f.refs.map((e) => ref(e))}
                          </li>
                        ))}
                      </ul>
                    </dd>
                  </div>
                )}
                {v.serverChanges.length > 0 && (
                  <div>
                    <dt>서버가 바꾼 분류</dt>
                    <dd>
                      <ul className="cut-findings" aria-label="서버가 바꾼 분류">
                        {v.serverChanges.map((s, i) => (
                          <li key={i}>{s}</li>
                        ))}
                      </ul>
                    </dd>
                  </div>
                )}
              </dl>
            </details>
          )}
        </dd>
        {c.unresolved.length > 0 && (
          <>
            <dt>미확정</dt>
            <dd className="cut-subtle">{c.unresolved.join(" · ")}</dd>
          </>
        )}
      </dl>

      {issues.length > 0 && (
        <ul className="cut-issues" aria-label="검수에서 걸린 이유">
          {issues.map((s, i) => (
            <li key={i}>검수에서 걸린 이유: {s}</li>
          ))}
        </ul>
      )}

      {(c.notes.length > 0 || c.notesApplied.length > 0) && (
        <div className="cut-notes">
          {c.notesApplied.length > 0 && (
            <p className="cut-subtle">
              이 버전에 반영한 메모 {c.notesApplied.length}개: {c.notesApplied.map((n) => n.text).join(" · ")}
            </p>
          )}
          {c.notes.length > 0 && (
            <>
              <p className="cut-subtle">메모 {c.notes.length}개 (다음 분석에 들어감)</p>
              <ul className="cut-note-list">
                {c.notes.map((n) => (
                  <li key={n.at}>{n.text}</li>
                ))}
              </ul>
            </>
          )}
        </div>
      )}

      <div className="cut-actions">
        <button
          type="button"
          className="link-button"
          aria-expanded={noteOpen}
          onClick={() => setNoteOpen((o) => !o)}
        >
          메모 추가
        </button>
        {c.confirmed ? (
          <>
            {/* Another boundary shown than the confirmed one: confirming it replaces the confirmed spans. */}
            {choice !== startBoundary(c) && (
              <button type="button" className="button cut-button" onClick={() => confirm.mutate(true)} disabled={confirm.isPending || spans.length === 0}>
                ‘{boundaryLabel[choice]}’ 경계로 다시 확정
              </button>
            )}
            <button type="button" className="link-button" onClick={() => confirm.mutate(false)} disabled={confirm.isPending}>
              확정 취소
            </button>
          </>
        ) : (
          <button type="button" className="button cut-button" onClick={() => confirm.mutate(true)} disabled={confirm.isPending || spans.length === 0}>
            컷 확정
          </button>
        )}
        {reanalyze}
      </div>
      {noteOpen && (
        <form
          className="cut-note-form"
          onSubmit={(e) => {
            e.preventDefault();
            if (noteText.trim()) note.mutate();
          }}
        >
          <label className="field-label" htmlFor={`cut-note-${c.planId}`}>
            메모 내용
          </label>
          <textarea
            id={`cut-note-${c.planId}`}
            value={noteText}
            maxLength={500}
            rows={2}
            placeholder="예: 두 번째 시도라 앞선 약속과 연결하면 안 됨"
            onChange={(e) => setNoteText(e.target.value)}
          />
          <p className="field-help">메모는 이 컴퓨터에만 저장되고, 다시 분석하면 계획 단계에 함께 보내 새 버전을 만듭니다. 확정한 버전은 그대로 둡니다.</p>
          <button type="submit" className="button cut-button" disabled={!noteText.trim() || note.isPending}>
            메모 저장
          </button>
        </form>
      )}
      {c.confirmed && (
        <p className="cut-subtle">파일 탭의 컷 확정본(EDL·CSV·editnest 타임라인 메모 JSON)에 들어갔습니다.</p>
      )}
      {(confirm.error ?? note.error) && (
        <p className="error" role="alert">
          {(confirm.error ?? note.error)!.message}
        </p>
      )}
    </div>
  );
}

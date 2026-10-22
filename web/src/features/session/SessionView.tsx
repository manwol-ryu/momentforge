import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type {
  Analysis,
  Candidate,
  Chat,
  CutBoundaryName,
  DetectWindow,
  Session,
  TimelineItem,
  TimelineSource,
  TimelineSpan,
  UnifiedTimeline,
} from "../../api/types";
import { AIReviewBar } from "../../shared/ai/AIReviewBar";
import { useAIEvents } from "../../shared/ai/useAIEvents";
import { useAppState } from "../../shared/appState";
import { foldAI, unexplained } from "../../shared/candidates/foldAI";
import { ListTools } from "../../shared/candidates/ListTools";
import {
  candidateNumbers,
  hasStrengths,
  keepRow,
  sortRows,
  strengthCounts,
  type SortOrder,
  type StrengthFilter,
} from "../../shared/candidates/strength";
import { sourceLabel, UnifiedList } from "../../shared/candidates/UnifiedList";
import { CutRequest } from "../../shared/candidates/CutRequest";
import { useCutView } from "../../shared/candidates/useCutView";
import { ChatList, currentChat } from "../../shared/chat/ChatList";
import { aiGroup, cutGroups, entriesIn, ExportFiles, olderCutFolders, transcriptGroup, type FileGroup } from "../../shared/files/ExportFiles";
import { timecode } from "../../shared/format";
import { JobPanel } from "../../shared/job/JobPanel";
import { RollingNumber } from "../../shared/motion/RollingNumber";
import { Segmented } from "../../shared/motion/Segmented";
import { Stats } from "../../shared/motion/Stats";
import { Tabs, type Tab } from "../../shared/motion/Tabs";
import { navigate, useDocumentTitle } from "../../shared/navigation";
import { createPlayhead, usePlayhead, type Playhead } from "../../shared/playhead";
import { VideoPlayer, type PlayerHandle } from "../../shared/player/VideoPlayer";
import { sessionKindText } from "../../shared/sessionText";
import { Timeline, type ExtraMarker, type TimelineScale } from "../../shared/timeline/Timeline";
import { WorkbenchSkeleton } from "../../shared/skeleton/WorkbenchSkeleton";
import { useJob } from "../../shared/useJob";
import { useStoredChoice } from "../../shared/useStoredChoice";
import { BundlePanel } from "./BundlePanel";
import { CommentsPanel } from "./CommentsPanel";
import { TranscriptPanel } from "./TranscriptPanel";

type PanelTab = "candidates" | "ai" | "transcript" | "comments" | "files";

/** How much of the timeline is drawn: the whole length, or a window around the playhead. */
type Zoom = "all" | "1800000" | "600000" | "120000";

const zoomText: Record<Zoom, string> = { all: "전체", "1800000": "30분", "600000": "10분", "120000": "2분" };

/** Keys typed into a field, a tab list or a choice belong to that control, not the page. */
function keyForControl(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable || target.closest('[role="tab"], [role="tablist"]')) return true;
  if (target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return true;
  return target instanceof HTMLInputElement && !["range", "checkbox", "button", "submit"].includes(target.type);
}

/**
 * A saved VOD as an editing workbench (docs/DESIGN-SYSTEM.md 4): the title
 * and job state on top, the video, the chat at the playhead and the reaction
 * timeline held in view, and the tools (candidates, AI review, comments,
 * files) in tabs beside them.
 */
export function SessionView({ id }: { id: string }) {
  const session = useQuery({ queryKey: ["session", id], queryFn: () => api.session(id) });
  const analysisId = session.data?.latestAnalysisId;
  const analysis = useQuery({
    queryKey: ["analysis", analysisId],
    queryFn: () => api.analysis(analysisId!),
    enabled: !!analysisId,
  });
  const isVOD = session.data?.kind === "vod";
  const timeline = useQuery({ queryKey: ["timeline", id, analysisId], queryFn: () => api.timeline(id), enabled: isVOD });
  const [tab, setTab] = useState<PanelTab>("candidates");
  // AI runs (automatic, or asked for) refresh what they change when they end.
  useAIEvents(id);
  const job = useJob();
  // The job that was already there when the page opened (null: none); later jobs are news.
  const [openedJobId, setOpenedJobId] = useState<string | null | undefined>(undefined);
  useEffect(() => {
    if (openedJobId === undefined && job.isSuccess) setOpenedJobId(job.data?.id || null);
  }, [openedJobId, job.isSuccess, job.data?.id]);
  const runningHere = !!job.data?.busy && job.data.sessionId === id;
  const [sawJobRun, setSawJobRun] = useState(false);
  useEffect(() => {
    if (runningHere) setSawJobRun(true);
  }, [runningHere]);
  const isLive = session.data?.kind === "live";
  useEffect(() => {
    if (isLive) navigate(`/live/${id}`, { replace: true });
  }, [isLive, id]);
  const ready = session.isSuccess && (!analysisId || analysis.isSuccess) && (!isVOD || timeline.isSuccess);
  useAppState(ready ? "ready" : session.isError || analysis.isError || timeline.isError ? "error" : "loading");
  useDocumentTitle(session.data?.title);

  const a = analysis.data;
  const tl = timeline.data;
  const items: TimelineItem[] = useMemo(
    () =>
      tl?.items ??
      (a?.candidates ?? []).map((c) => ({
        id: "rule:" + c.id, source: "rule" as const, originId: c.id, anchorMs: c.clipStartMs,
        clipStartMs: c.clipStartMs, clipEndMs: c.clipEndMs, title: c.reasons.join(" · "), watchUrl: c.watchUrl,
      })),
    [tl, a],
  );
  const folded = useMemo(() => foldAI(items), [items]);

  const s = session.data;
  if (session.isError) {
    return (
      <div className="page">
        <p className="error" role="alert">
          {session.error.message}
        </p>
      </div>
    );
  }
  if (!s) return <WorkbenchSkeleton />;

  const jobHere = job.data?.id && job.data.sessionId === id ? job.data : undefined;
  // A job that ended before this page opened is old news; one that runs or ends while watching stays to say so.
  const showJob =
    jobHere &&
    (jobHere.busy || jobHere.phase !== "done" || sawJobRun || (openedJobId !== undefined && jobHere.id !== openedJobId));
  const busy = job.data?.busy ?? false;
  const itemCount = folded.rows.length;
  // A ranked analysis says how many candidates stand out; an older one, how busy the busiest window was.
  const ranked = hasStrengths(a?.candidates ?? []);
  const strong = ranked ? strengthCounts(a?.candidates ?? []).strong : 0;
  const peak = ranked ? 0 : (a?.windows ?? []).reduce((m, w) => Math.max(m, w.n), 0);
  const tools: Tab<PanelTab>[] = [];
  if (isVOD) {
    tools.push({ id: "transcript", label: "대본", content: null }); // filled in Review, which owns the playhead
    tools.push({ id: "comments", label: "댓글", content: <CommentsPanel s={s} busy={busy} /> });
  }
  tools.push({
    id: "files",
    label: "파일",
    content: (
      <>
        <Files sessionId={s.id} analysisId={analysisId} timeline={tl} updatedAt={s.updatedAt} longForm={s.durationMs >= 3_600_000} />
        {isVOD && <BundlePanel s={s} busy={busy} />}
      </>
    ),
  });

  return (
    <div className="ws">
      <header className="ws-head">
        <div className="ws-heading">
          <h1 className="ws-title">{s.title}</h1>
          <p className="ws-meta">{s.isDemo ? "실제 방송이 아닌 예시 채팅" : sessionKindText(s)}</p>
        </div>
        <Stats
          items={[
            { label: "채팅", value: <><RollingNumber value={s.chatCount} />건</> },
            { label: "편집 후보", value: <><RollingNumber value={itemCount} />개</> },
            ranked
              ? { label: "강한 후보", value: <><RollingNumber value={strong} />개</> }
              : { label: "가장 활발한 10초", value: peak ? <><RollingNumber value={peak} />건</> : "-" },
            { label: "길이", value: <span className="timecode">{timecode(s.durationMs, s.durationMs >= 3_600_000)}</span> },
          ]}
        />
        {s.watchUrl && (
          <a className="button button-quiet ws-action" href={s.watchUrl} target="_blank" rel="noreferrer">
            {s.platformName}에서 열기
          </a>
        )}
        {(s.state !== "finished" || showJob || analysis.isError) && (
          <div className="ws-notices">
            {s.state !== "finished" && <ResumeNotice s={s} busy={busy} />}
            {showJob && <JobPanel job={jobHere} showResultLink={false} />}
            {analysis.isError && (
              <p className="error" role="alert">
                {analysis.error.message}
              </p>
            )}
          </div>
        )}
      </header>
      <Review
        s={s}
        a={a}
        items={items}
        rows={folded.rows}
        aiFor={folded.aiFor}
        busy={busy}
        tab={tab}
        onTab={setTab}
        tools={tools}
      />
    </div>
  );
}

function ResumeNotice({ s, busy }: { s: Session; busy: boolean }) {
  const resume = useMutation({ mutationFn: () => api.resume(s.id, false) });
  if (s.state === "collecting" && busy) return null;
  const commentsOnly = s.state === "comments_only";
  return (
    <div className="notice">
      <p>
        {commentsOnly ? (
          "영상 댓글만 받은 작업입니다. 채팅도 받아 편집 후보를 찾으려면 채팅 수집을 누르세요."
        ) : (
          <>
            {s.errorMessage ?? "수집이 끝나지 않았습니다."}{" "}
            <span className="timecode">{timecode(s.cursor, s.durationMs >= 3_600_000)}</span>까지 저장되어 있습니다.
          </>
        )}
      </p>
      {s.canResume && (
        <p className="actions">
          <button type="button" className="button" onClick={() => resume.mutate()} disabled={busy || resume.isPending}>
            {commentsOnly ? "채팅 수집" : "이어받기"}
          </button>
        </p>
      )}
      {resume.isError && (
        <p className="error" role="alert">
          {resume.error.message}
        </p>
      )}
    </div>
  );
}

type SourceFilter = "all" | TimelineSource;

const NO_CHATS: Chat[] = [];
const NO_WINDOWS: DetectWindow[] = [];

/** The next moment in time order after the playhead (with a second's slack, so the current one is not chosen again). */
function nextItem(ordered: TimelineItem[], ms: number): TimelineItem | undefined {
  return ordered.find((i) => i.clipStartMs > ms + 1000);
}

/** The previous moment before the playhead (two seconds' slack, so a moment just started counts as the current one). */
function prevItem(ordered: TimelineItem[], ms: number): TimelineItem | undefined {
  for (let i = ordered.length - 1; i >= 0; i--) if (ordered[i]!.clipStartMs < ms - 2000) return ordered[i];
  return undefined;
}

interface ReviewProps {
  s: Session;
  a?: Analysis;
  /** Every moment of every source, and the same with AI explanations folded under their candidates. */
  items: TimelineItem[];
  rows: TimelineItem[];
  aiFor: Map<string, TimelineItem[]>;
  busy: boolean;
  tab: PanelTab;
  onTab: (tab: PanelTab) => void;
  /** The panel's other tabs, after the candidates. */
  tools: Tab<PanelTab>[];
}

function Review({
  s,
  a,
  items,
  rows,
  aiFor,
  busy,
  tab,
  onTab,
  tools,
}: ReviewProps) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // The boundary candidate shown per cut plan, and the spans the timeline draws for the chosen row.
  const cut = useCutView(items, aiFor, selectedId);
  const chooseCut = (planId: string, name: CutBoundaryName, itemId: string) => {
    cut.choose(planId, name);
    setSelectedId(itemId);
  };
  const [filter, setFilter] = useState<SourceFilter>("all");
  const [sort, setSort] = useStoredChoice<SortOrder>("mf.candidates.sort", ["time", "strength"], "time");
  const [strength, setStrength] = useStoredChoice<StrengthFilter>("mf.candidates.strength", ["all", "strong_medium"], "all");
  // The playhead: the video's time, or without a video, the moment last chosen on the timeline or in the list.
  // It lives outside React state, so a playing video re-renders only what shows the time.
  const [playhead] = useState(createPlayhead);
  const [follow, setFollow] = useState(true);
  const player = useRef<PlayerHandle>(null);
  const durationMs = a?.durationMs ?? s.durationMs;
  const longForm = durationMs >= 3_600_000;
  const candidates = useMemo(() => new Map((a?.candidates ?? []).map((c) => [c.id, c])), [a]);
  const numbers = useMemo(() => candidateNumbers(a?.candidates ?? []), [a]);
  const ranked = useMemo(() => hasStrengths(a?.candidates ?? []), [a]);
  const counts = useMemo(() => strengthCounts(a?.candidates ?? []), [a]);
  const sources = [...new Set(rows.map((i) => i.source))];
  const shown = useMemo(() => {
    const kept = rows.filter((i) => (filter === "all" || i.source === filter) && keepRow(i, candidates, ranked ? strength : "all"));
    return sortRows(kept, candidates, ranked ? sort : "time");
  }, [rows, filter, candidates, ranked, strength, sort]);
  // Previous and next follow time order whatever the list's order.
  const ordered = useMemo(() => [...shown].sort((x, y) => x.clipStartMs - y.clipStartMs), [shown]);
  // The timeline marks what the list shows, in time order, with the list's numbers.
  const markerCandidates = useMemo(() => {
    const visible = new Set(shown.filter((i) => i.source === "rule").map((i) => i.originId));
    return (a?.candidates ?? []).filter((c) => visible.has(c.id));
  }, [a, shown]);
  const extraMarkers = useMemo(() => {
    const visible = new Set(shown.map((i) => i.id));
    const counter: Partial<Record<TimelineSource, number>> = {};
    const out: ExtraMarker[] = [];
    for (const i of rows) {
      if (i.source === "rule") continue;
      counter[i.source] = (counter[i.source] ?? 0) + 1;
      if (!visible.has(i.id)) continue;
      out.push({
        id: i.id, ms: i.anchorMs, kind: i.source,
        label: `${sourceLabel[i.source]} ${counter[i.source]}, ${timecode(i.anchorMs, longForm)}`,
      });
    }
    return out;
  }, [rows, shown, longForm]);
  // Jump to a time: the video plays from there, or without a video the chat shows it.
  const goTo = (ms: number) => {
    playhead.set(ms);
    if (s.canPlay) player.current?.seek(ms);
  };
  const goToRef = useRef(goTo);
  goToRef.current = goTo;
  // Choosing a moment opens its details and jumps to its clip; choosing it again closes it.
  const select = (itemId: string) => {
    if (selectedId === itemId) {
      setSelectedId(null);
      return;
    }
    setSelectedId(itemId);
    const it = items.find((i) => i.id === itemId);
    if (it) goTo(it.clipStartMs);
  };
  const seek = (ms: number, itemId: string) => {
    setSelectedId(itemId);
    goTo(ms);
  };
  const jump = (it: TimelineItem | undefined) => {
    if (!it) return;
    setSelectedId(it.id);
    goTo(it.clipStartMs);
  };
  const jumpRef = useRef({ next: () => {}, prev: () => {} });
  jumpRef.current = { next: () => jump(nextItem(ordered, playhead.get())), prev: () => jump(prevItem(ordered, playhead.get())) };
  // N and P anywhere on the page (by key position, so a Korean keyboard layout works too).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey || keyForControl(e.target)) return;
      if (e.code === "KeyN") {
        e.preventDefault();
        jumpRef.current.next();
      } else if (e.code === "KeyP") {
        e.preventDefault();
        jumpRef.current.prev();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  // The chosen moment stays in sight in the list, however it was chosen.
  useEffect(() => {
    if (!selectedId) return;
    const row = document.querySelector<HTMLElement>(`[data-item="${CSS.escape(selectedId)}"]`);
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    row?.scrollIntoView({ block: "nearest", behavior: reduce ? "auto" : "smooth" });
  }, [selectedId]);
  const scrub = (ms: number) => {
    playhead.set(ms);
    player.current?.seek(ms, false);
  };
  // A cut plan's spans played one after another: when the playhead passes a span's end, the next one starts.
  // Nothing shows the queue, so it is a ref the playhead's listener reads.
  const queue = useRef<{ spans: TimelineSpan[]; at: number } | null>(null);
  const playSpans = (spans: TimelineSpan[], itemId: string) => {
    if (spans.length === 0) return;
    setSelectedId(itemId);
    queue.current = null; // the jump below is not judged against an earlier queue
    goTo(spans[0]!.startMs);
    queue.current = { spans, at: 0 };
  };
  useEffect(
    () =>
      playhead.subscribe(() => {
        const q = queue.current;
        const cur = q?.spans[q.at];
        if (!q || !cur) return;
        const nowMs = playhead.get();
        if (nowMs < cur.startMs - 1000 || nowMs > cur.endMs + 5000) {
          queue.current = null; // the user went elsewhere
          return;
        }
        if (nowMs < cur.endMs) return;
        const next = q.spans[q.at + 1];
        queue.current = next ? { spans: q.spans, at: q.at + 1 } : null;
        if (next) goToRef.current(next.startMs);
      }),
    [playhead],
  );

  const showTimeline = (!!a || items.length > 0) && durationMs > 0;
  const noResult = s.state === "comments_only"
    ? "영상 댓글에서 시각을 찾지 못했습니다. 위의 채팅 수집을 누르면 채팅으로 편집 후보를 찾습니다."
    : "아직 분석 결과가 없습니다. 수집을 마치면 편집 후보를 찾습니다.";
  const isVOD = s.kind === "vod";

  const candidatesPanel: ReactNode = (
    <>
      <section className="panel-section" aria-labelledby="candidates-title">
        <div className="section-head">
          <h2 id="candidates-title" className="section-title">
            편집 후보 <RollingNumber value={shown.length} />개
          </h2>
          {sources.length > 1 && (
            <Segmented
              name="candidate-source"
              legend="후보 출처"
              hideLegend
              value={filter}
              onChange={setFilter}
              options={[
                { value: "all" as SourceFilter, label: "모든 출처" },
                ...sources.map((src) => ({ value: src as SourceFilter, label: sourceLabel[src] })),
              ]}
            />
          )}
        </div>
        {ranked && <ListTools counts={counts} sort={sort} onSort={setSort} filter={strength} onFilter={setStrength} />}
        {isVOD && <AIReviewBar s={s} unexplained={unexplained({ rows, aiFor })} busy={busy} />}
        {shown.length > 0 && (
          <p className="section-hint">
            {s.canPlay ? "후보를 고르면 영상이 그 구간부터 재생되고 근거 채팅이 펼쳐집니다." : "후보를 고르면 그 구간의 채팅과 근거가 펼쳐집니다."}
          </p>
        )}
        {shown.length === 0 ? (
          <p className="empty">
            {rows.length > 0
              ? "고른 조건에 맞는 후보가 없습니다. 위에서 출처나 강도를 넓혀 보세요."
              : a || items.length > 0
                ? "반응이 몰린 구간을 찾지 못했습니다. 더 작은 반응까지 보려면 '항상 가장 민감하게'로 다시 분석하세요."
                : noResult}
          </p>
        ) : (
          <UnifiedList
            items={shown}
            candidates={candidates}
            numbers={numbers}
            scoreThreshold={a?.scoreThreshold}
            aiFor={aiFor}
            selectedId={selectedId}
            onSelect={select}
            longForm={longForm}
            canPlay={s.canPlay}
            platformName={s.platformName}
            onSeek={seek}
            onPlaySpans={playSpans}
            cutChoice={cut.choice}
            onCutChoice={chooseCut}
            renderCut={
              isVOD
                ? (it, name, planId) => (
                    <CutRequest sessionId={s.id} itemId={it.id} name={name} planId={planId} label={planId ? "다시 분석" : "AI 컷 분석"} />
                  )
                : undefined
            }
            renderRuleDetail={(c) => (a ? <CandidateContext analysisId={a.id} c={c} longForm={longForm} onPick={goTo} /> : null)}
          />
        )}
        {a && <Reanalyze s={s} a={a} busy={busy} />}
      </section>
    </>
  );

  return (
    <>
      <section className="ws-stage" aria-label={s.canPlay ? "영상, 채팅, 타임라인" : "채팅과 타임라인"}>
        <div className="stage-media" data-video={s.canPlay || undefined}>
          {s.canPlay && (
            <div className="stage-video card">
              <div className="card-head">
                <h2 className="stage-title">영상</h2>
                <PlayheadTime playhead={playhead} durationMs={durationMs} longForm={longForm} />
              </div>
              <div className="card-body card-screen">
                <VideoPlayer
                  ref={player}
                  sessionId={s.id}
                  transport
                  fallback={s.watchUrl ? `위의 ‘${s.platformName}에서 열기’로 볼 수 있습니다.` : undefined}
                  onTime={(ms) => {
                    if (Math.abs(playhead.get() - ms) >= 250) playhead.set(ms);
                  }}
                />
              </div>
            </div>
          )}
          <div className="stage-chat card">
            <div className="card-head">
              <h2 className="stage-title">{s.canPlay ? "재생 위치 채팅" : "고른 위치 채팅"}</h2>
              {s.canPlay ? (
                <label className="check check-inline">
                  <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />
                  따라가기
                </label>
              ) : (
                <PlayheadTime playhead={playhead} longForm={longForm} />
              )}
            </div>
            <div className="card-body">
              <PlayheadChat
                s={s}
                analysisId={a?.id}
                playhead={playhead}
                follow={!s.canPlay || follow}
                longForm={longForm}
                label={s.canPlay ? "재생 위치 주변 채팅" : "고른 위치 주변 채팅"}
                onPick={goTo}
              />
            </div>
            <p className="card-foot">
              {s.canPlay
                ? "채팅을 누르면 영상이 그 시각으로 갑니다. 재생 위치 앞뒤 30초의 채팅을 보여 줍니다."
                : "영상이 없는 작업입니다. 타임라인을 누르거나 후보를 고르면 그 시각 앞뒤 채팅을 보여 줍니다."}
            </p>
          </div>
        </div>
        {showTimeline && (
          <TimelineCard
            a={a}
            playhead={playhead}
            durationMs={durationMs}
            longForm={longForm}
            candidates={markerCandidates}
            numbers={numbers}
            extraMarkers={extraMarkers}
            cutSpans={cut.spans}
            selectedId={selectedId?.startsWith("rule:") ? selectedId.slice(5) : selectedId}
            onSelect={(markerId) => select(markerId.includes(":") ? markerId : "rule:" + markerId)}
            onScrub={scrub}
            scrubLabel={s.canPlay ? "재생 위치" : "채팅 볼 위치"}
            ordered={rows.length > 0 ? ordered : undefined}
            onJump={jump}
          />
        )}
      </section>
      <aside className="ws-panel" aria-label="작업 도구">
        <Tabs
          label="작업 도구"
          className="panel-tabs"
          value={tab}
          onChange={onTab}
          tabs={[
            {
              id: "candidates",
              label: (
                <>
                  후보 <span className="tab-count">{rows.length}</span>
                </>
              ),
              content: candidatesPanel,
            },
            ...tools.map((t) =>
              t.id === "transcript"
                ? { ...t, content: <TranscriptPanel s={s} busy={busy} playhead={playhead} durationMs={durationMs} onPick={goTo} /> }
                : t,
            ),
          ]}
        />
      </aside>
    </>
  );
}

/** The playhead as a timecode, re-rendered once a second. */
function PlayheadTime({ playhead, durationMs, longForm }: { playhead: Playhead; durationMs?: number; longForm: boolean }) {
  const second = usePlayhead(playhead, (ms) => Math.floor(ms / 1000));
  return (
    <span className="stage-time timecode">
      {timecode(second * 1000, longForm)}
      {durationMs !== undefined && ` / ${timecode(durationMs, longForm)}`}
    </span>
  );
}

interface PlayheadChatProps {
  s: Session;
  analysisId?: string;
  playhead: Playhead;
  follow: boolean;
  longForm: boolean;
  label: string;
  onPick: (ms: number) => void;
}

/** Chat around the playhead, fetched per 10-second step; the list re-renders when the current message changes. */
function PlayheadChat({ s, analysisId, playhead, follow, longForm, label, onPick }: PlayheadChatProps) {
  const step = usePlayhead(playhead, (ms) => Math.floor(ms / 10_000) * 10_000);
  const near = useQuery({
    queryKey: ["chatsAt", s.id, analysisId, step],
    queryFn: () => api.chatsAt(s.id, step + 5_000, analysisId),
    enabled: s.chatCount > 0,
    placeholderData: keepPreviousData,
  });
  const chats = near.data?.chats ?? NO_CHATS;
  // The current message's time (-1 before the first), so the list only hears about changes.
  const currentMs = usePlayhead(playhead, (ms) => {
    const i = currentChat(chats, ms);
    return i < 0 ? -1 : chats[i]!.offsetMs;
  });
  return <ChatList chats={chats} currentMs={currentMs} follow={follow} longForm={longForm} label={label} onPick={onPick} />;
}

interface TimelineCardProps {
  a?: Analysis;
  playhead: Playhead;
  durationMs: number;
  longForm: boolean;
  /** The rule candidates the list shows (its filters applied), in time order. */
  candidates: Candidate[];
  numbers: Map<string, number>;
  extraMarkers: ExtraMarker[];
  cutSpans: TimelineSpan[];
  selectedId: string | null;
  onSelect: (markerId: string) => void;
  onScrub: (ms: number) => void;
  scrubLabel: string;
  /** The list's rows in time order, for the previous and next buttons; absent without rows. */
  ordered?: TimelineItem[];
  onJump: (it: TimelineItem | undefined) => void;
}

/**
 * The reaction timeline's card: zoom, the bars' scale (against the usual
 * for that time, or messages), the way to the previous or next candidate
 * and the playhead's time. It follows the playhead itself, so the rest of
 * the workbench does not re-render while a video plays.
 */
function TimelineCard({
  a,
  playhead,
  durationMs,
  longForm,
  candidates,
  numbers,
  extraMarkers,
  cutSpans,
  selectedId,
  onSelect,
  onScrub,
  scrubLabel,
  ordered,
  onJump,
}: TimelineCardProps) {
  const nowMs = usePlayhead(playhead, (ms) => ms);
  // Zoomed in, the drawn window pages along with the playhead (it moves when the playhead leaves it).
  const [zoom, setZoom] = useState<Zoom>("all");
  const zoomSpan = zoom === "all" ? durationMs : Math.min(durationMs, Number(zoom));
  const [viewStart, setViewStart] = useState(0);
  useEffect(() => {
    if (zoom === "all") return;
    setViewStart((start) => {
      if (nowMs >= start && nowMs <= start + zoomSpan) return start;
      return Math.max(0, Math.min(durationMs - zoomSpan, nowMs - zoomSpan * 0.1));
    });
  }, [zoom, zoomSpan, nowMs, durationMs]);
  const chooseZoom = (z: Zoom) => {
    setZoom(z);
    const span = z === "all" ? durationMs : Math.min(durationMs, Number(z));
    setViewStart(Math.max(0, Math.min(durationMs - span, playhead.get() - span / 2)));
  };
  // In this order (object keys that look like numbers would come first).
  const zoomOptions = (["all", "1800000", "600000", "120000"] as Zoom[]).filter((z) => z === "all" || Number(z) < durationMs * 0.75);
  const windows = a?.windows ?? NO_WINDOWS;
  const scored = useMemo(() => windows.some((w) => w.score !== undefined), [windows]);
  const [scale, setScale] = useStoredChoice<TimelineScale>("mf.timeline.scale", ["score", "count"], "score");
  const next = ordered && nextItem(ordered, nowMs);
  const prev = ordered && prevItem(ordered, nowMs);
  return (
    <div className="stage-timeline card">
      <div className="card-head">
        <h2 id="timeline-title" className="stage-title">
          반응 타임라인
        </h2>
        <div className="timeline-tools">
          {scored && (
            <Segmented
              name="timeline-scale"
              legend="막대 기준"
              hideLegend
              value={scale}
              onChange={setScale}
              options={[
                { value: "score" as TimelineScale, label: "평소 대비" },
                { value: "count" as TimelineScale, label: "채팅 수" },
              ]}
            />
          )}
          {zoomOptions.length > 1 && (
            <Segmented
              name="timeline-zoom"
              legend="타임라인 확대"
              hideLegend
              value={zoom}
              onChange={chooseZoom}
              options={zoomOptions.map((z) => ({ value: z, label: zoomText[z] }))}
            />
          )}
          {ordered && (
            <span className="timeline-step">
              <button type="button" className="transport-button" onClick={() => onJump(prev)} disabled={!prev} title="단축키 P">
                이전 후보
              </button>
              <button type="button" className="transport-button" onClick={() => onJump(next)} disabled={!next} title="단축키 N">
                다음 후보
              </button>
            </span>
          )}
          <span className="stage-time timecode">
            {timecode(nowMs, longForm)} / {timecode(durationMs, longForm)}
          </span>
        </div>
      </div>
      <div className="card-body">
        <Timeline
          durationMs={durationMs}
          windows={windows}
          stepMs={a?.stepMs || 5000}
          windowMs={a?.windowMs || 10_000}
          scale={scored ? scale : "count"}
          scoreThreshold={a?.scoreThreshold}
          candidates={candidates}
          numbers={numbers}
          extraMarkers={extraMarkers}
          cutSpans={cutSpans}
          selectedId={selectedId}
          onSelect={onSelect}
          playheadMs={nowMs}
          onScrub={onScrub}
          scrubLabel={scrubLabel}
          viewStartMs={zoom === "all" ? 0 : viewStart}
          viewEndMs={zoom === "all" ? durationMs : viewStart + zoomSpan}
        />
      </div>
    </div>
  );
}

function CandidateContext({ analysisId, c, longForm, onPick }: { analysisId: string; c: Candidate; longForm: boolean; onPick: (ms: number) => void }) {
  const chats = useInfiniteQuery({
    queryKey: ["candidateChats", analysisId, c.id],
    queryFn: ({ pageParam }) => api.candidateChats(analysisId, c.id, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => last.nextOffset ?? undefined,
  });
  const all = chats.data?.pages.flatMap((p) => p.chats) ?? [];
  const total = chats.data?.pages[0]?.total ?? 0;
  return (
    <div className="context">
      <p className="evidence-title">
        편집 구간 채팅 {total.toLocaleString("ko-KR")}건 (* 판정 근거)
      </p>
      <ChatList chats={all} longForm={longForm} label="편집 구간 채팅" onPick={onPick} />
      {chats.hasNextPage && (
        <button type="button" className="link-button" onClick={() => chats.fetchNextPage()} disabled={chats.isFetchingNextPage}>
          채팅 더 보기
        </button>
      )}
    </div>
  );
}

function Reanalyze({ s, a, busy }: { s: Session; a: Analysis; busy: boolean }) {
  const [sensitive, setSensitive] = useState(a.sensitive);
  const run = useMutation({ mutationFn: () => api.reanalyze(s.id, sensitive) });
  return (
    <div className="reanalyze">
      <label className="check">
        <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
        항상 가장 민감하게
      </label>
      <button type="button" className="button button-quiet" onClick={() => run.mutate()} disabled={busy || run.isPending}>
        다시 분석
      </button>
      {run.isError && (
        <p className="error" role="alert">
          {run.error.message}
        </p>
      )}
    </div>
  );
}

/** What each file of a VOD's own folders holds, by its name. */
const chatText: Record<string, string> = {
  "chat.txt": "시간순 채팅 TXT",
  "chat.received.txt": "받은 순서대로 채팅 TXT",
  "chat.jsonl": "채팅 JSONL",
};
const allSourcesText: Record<string, string> = {
  "timeline.txt": "모든 출처 편집 후보 TXT",
  "timeline.csv": "모든 출처 편집 후보 CSV",
  "timeline.json": "모든 출처 편집 후보 JSON",
};
const ruleText: Record<string, string> = {
  "timeline.txt": "자동 탐지 편집 후보 TXT",
  "timeline.csv": "자동 탐지 편집 후보 CSV",
  "timeline.json": "자동 탐지 편집 후보 JSON",
  "candidates.txt": "자동 탐지 편집 후보 TXT",
  "candidates.csv": "자동 탐지 편집 후보 CSV",
  "candidates.json": "자동 탐지 편집 후보 JSON",
  "analysis-manifest.json": "분석 기록 JSON",
};
const commentText: Record<string, string> = {
  "recommendations.json": "시청자 추천 JSON",
  "comments.jsonl": "영상 댓글 JSONL",
  "manifest.json": "댓글 수집 기록 JSON",
};

interface FilesProps {
  sessionId: string;
  analysisId?: string;
  timeline?: UnifiedTimeline;
  updatedAt: string;
  longForm: boolean;
}

/**
 * The files of a VOD in groups, the ones for editing first: the confirmed
 * cuts, the candidates (all sources, then the automatic ones), the chat,
 * the viewers' picks, the AI review, each cut analysis, the transcript and
 * the collection record. Only the newest analysis, comment capture and AI
 * review are listed; older ones stay in the data folder.
 */
function Files({ sessionId, analysisId, timeline, updatedAt, longForm }: FilesProps) {
  const files = useQuery({
    queryKey: ["files", sessionId, analysisId, timeline?.captureId, timeline?.aiRunId, timeline?.items.filter((i) => i.kind === "cut").length, updatedAt],
    queryFn: () => api.files(sessionId),
  });
  const all = files.data ?? [];
  const items = timeline?.items ?? [];
  // Replaced cut analyses are named from the saved plans, asked for only when there are any.
  const older = olderCutFolders(all, items);
  const saved = useQuery({
    queryKey: ["cutPlans", sessionId, older.join(",")],
    queryFn: () => api.cutPlans(sessionId),
    enabled: older.length > 0,
  });
  const [confirmed, ...plans] = cutGroups(all, items, longForm, saved.data?.plans);
  const transcript = useQuery({ queryKey: ["transcript", sessionId], queryFn: () => api.transcript(sessionId) });
  const groups: FileGroup[] = [
    confirmed!,
    {
      id: "candidates",
      title: "편집 후보",
      help: "모든 출처는 자동 탐지·시청자 추천·AI 추천을 시각순으로 합친 목록이고, 자동 탐지는 채팅 분석이 찾은 후보와 근거입니다.",
      entries: [...entriesIn(all, "timeline/", allSourcesText), ...(analysisId ? entriesIn(all, analysisId + "/", ruleText) : [])],
    },
    { id: "chat", title: "채팅", entries: entriesIn(all, "", chatText) },
    {
      id: "comments",
      title: "시청자 추천 (영상 댓글)",
      entries: timeline?.captureId ? entriesIn(all, `comments/${timeline.captureId}/`, commentText) : [],
    },
    aiGroup(all, timeline?.aiRunId),
    ...plans,
    transcriptGroup(all, transcript.data?.id),
    { id: "record", title: "수집 기록", entries: entriesIn(all, "", { "manifest.json": "수집 기록 JSON" }) },
  ];
  return (
    <section className="panel-section" aria-labelledby="files-title">
      <h2 id="files-title" className="section-title">
        내보낸 파일
      </h2>
      {files.isError ? (
        <p className="error" role="alert">
          {files.error.message}
        </p>
      ) : (
        <ExportFiles sessionId={sessionId} groups={groups} empty={files.isPending ? "파일 목록을 불러오는 중" : "아직 만든 파일이 없습니다."} />
      )}
      <p className="field-help">후보별 앞뒤 채팅(markers 폴더)은 데이터 폴더의 exports에서 볼 수 있습니다.</p>
    </section>
  );
}

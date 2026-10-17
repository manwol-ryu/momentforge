import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Analysis, Candidate, ExportFile, Session, TimelineItem, TimelineSource, UnifiedTimeline } from "../../api/types";
import { AIComparePanel } from "../../shared/ai/AIComparePanel";
import { AIPanel } from "../../shared/ai/AIPanel";
import { useAppState } from "../../shared/appState";
import { sourceLabel, UnifiedList } from "../../shared/candidates/UnifiedList";
import { ChatList } from "../../shared/chat/ChatList";
import { formatSize, timecode } from "../../shared/format";
import { JobPanel } from "../../shared/job/JobPanel";
import { RollingNumber } from "../../shared/motion/RollingNumber";
import { Segmented } from "../../shared/motion/Segmented";
import { Stats } from "../../shared/motion/Stats";
import { Tabs, type Tab } from "../../shared/motion/Tabs";
import { navigate, useDocumentTitle } from "../../shared/navigation";
import { VideoPlayer, type PlayerHandle } from "../../shared/player/VideoPlayer";
import { sessionKindText } from "../../shared/sessionText";
import { Timeline } from "../../shared/timeline/Timeline";
import { WorkbenchSkeleton } from "../../shared/skeleton/WorkbenchSkeleton";
import { useJob } from "../../shared/useJob";
import { CommentsPanel } from "./CommentsPanel";

type PanelTab = "candidates" | "ai" | "comments" | "files";

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
  const [aiSelection, setAISelection] = useState<Set<string>>(() => new Set());
  const toggleAI = (itemId: string) =>
    setAISelection((cur) => {
      const next = new Set(cur);
      if (!next.delete(itemId)) next.add(itemId);
      return next;
    });
  const [tab, setTab] = useState<PanelTab>("candidates");
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
  const hasResult = !!analysis.data || (timeline.data?.items.length ?? 0) > 0;
  const itemCount = timeline.data?.items.length ?? analysis.data?.candidates.length ?? 0;
  const peak = Math.max(0, ...(analysis.data?.windows ?? []).map((w) => w.n));
  const tools: Tab<PanelTab>[] = [];
  if (isVOD) {
    tools.push({
      id: "ai",
      label: "AI 검토",
      content: hasResult ? (
        <>
          <AIPanel s={s} selection={[...aiSelection]} />
          <AIComparePanel s={s} />
        </>
      ) : (
        <p className="empty">편집 후보가 생기면 고른 후보를 AI로 검토할 수 있습니다.</p>
      ),
    });
    tools.push({ id: "comments", label: "댓글", content: <CommentsPanel s={s} busy={busy} /> });
  }
  tools.push({
    id: "files",
    label: "파일",
    content: <Files sessionId={s.id} analysisId={analysisId} timeline={timeline.data} updatedAt={s.updatedAt} />,
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
            { label: "가장 활발한 10초", value: peak ? <><RollingNumber value={peak} />건</> : "-" },
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
        a={analysis.data}
        tl={timeline.data}
        busy={busy}
        aiSelection={aiSelection}
        onToggleAI={toggleAI}
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

interface ReviewProps {
  s: Session;
  a?: Analysis;
  tl?: UnifiedTimeline;
  busy: boolean;
  aiSelection: Set<string>;
  onToggleAI: (itemId: string) => void;
  tab: PanelTab;
  onTab: (tab: PanelTab) => void;
  /** The panel's other tabs, after the candidates. */
  tools: Tab<PanelTab>[];
}

function Review({ s, a, tl, busy, aiSelection, onToggleAI, tab, onTab, tools }: ReviewProps) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [filter, setFilter] = useState<SourceFilter>("all");
  // The playhead: the video's time, or without a video, the moment last chosen on the timeline or in the list.
  const [nowMs, setNowMs] = useState(0);
  const [follow, setFollow] = useState(true);
  const player = useRef<PlayerHandle>(null);
  const durationMs = a?.durationMs ?? s.durationMs;
  const longForm = durationMs >= 3_600_000;
  const candidates = useMemo(() => new Map((a?.candidates ?? []).map((c) => [c.id, c])), [a]);
  const items: TimelineItem[] = useMemo(
    () =>
      tl?.items ??
      (a?.candidates ?? []).map((c) => ({
        id: "rule:" + c.id, source: "rule" as const, originId: c.id, anchorMs: c.clipStartMs,
        clipStartMs: c.clipStartMs, clipEndMs: c.clipEndMs, title: c.reasons.join(" · "), watchUrl: c.watchUrl,
      })),
    [tl, a],
  );
  const sources = [...new Set(items.map((i) => i.source))];
  const shown = filter === "all" ? items : items.filter((i) => i.source === filter);
  const select = (itemId: string) => {
    const on = selectedId !== itemId;
    setSelectedId(on ? itemId : null);
    const it = items.find((i) => i.id === itemId);
    if (on && it && !s.canPlay) setNowMs(it.clipStartMs);
  };
  const seek = (ms: number, itemId: string) => {
    setSelectedId(itemId);
    setNowMs(ms);
    player.current?.seek(ms);
  };
  const scrub = (ms: number) => {
    setNowMs(ms);
    player.current?.seek(ms, false);
  };

  // Chat near the playhead, fetched per 10-second step.
  const step = Math.floor(nowMs / 10_000) * 10_000;
  const playhead = useQuery({
    queryKey: ["chatsAt", s.id, a?.id, step],
    queryFn: () => api.chatsAt(s.id, step + 5_000, a?.id),
    enabled: s.chatCount > 0,
    placeholderData: keepPreviousData,
  });
  const counter: Partial<Record<TimelineSource, number>> = {};
  const extraMarkers = items
    .filter((i) => i.source !== "rule")
    .map((i) => {
      counter[i.source] = (counter[i.source] ?? 0) + 1;
      return {
        id: i.id, ms: i.anchorMs, kind: i.source as "viewer_comment" | "ai",
        label: `${sourceLabel[i.source]} ${counter[i.source]}, ${timecode(i.anchorMs, longForm)}`,
      };
    });
  const showTimeline = (!!a || items.length > 0) && durationMs > 0;
  const noResult = s.state === "comments_only"
    ? "영상 댓글에서 시각을 찾지 못했습니다. 위의 채팅 수집을 누르면 채팅으로 편집 후보를 찾습니다."
    : "아직 분석 결과가 없습니다. 수집을 마치면 편집 후보를 찾습니다.";

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
        {shown.length === 0 ? (
          <p className="empty">
            {a || items.length > 0
              ? "반응이 몰린 구간을 찾지 못했습니다. 더 작은 반응까지 보려면 '항상 가장 민감하게'로 다시 분석하세요."
              : noResult}
          </p>
        ) : (
          <UnifiedList
            items={shown}
            candidates={candidates}
            selectedId={selectedId}
            onSelect={select}
            longForm={longForm}
            canPlay={s.canPlay}
            platformName={s.platformName}
            onSeek={seek}
            aiSelection={aiSelection}
            onToggleAI={onToggleAI}
            renderRuleDetail={(c) => (a ? <CandidateContext analysisId={a.id} c={c} longForm={longForm} /> : null)}
          />
        )}
        {a && <Reanalyze s={s} a={a} busy={busy} />}
      </section>
      {aiSelection.size > 0 && tools.some((t) => t.id === "ai") && (
        <p className="pick-bar">
          <span>AI 검토에 고른 후보 {aiSelection.size}개</span>
          <button type="button" className="link-button" onClick={() => onTab("ai")}>
            AI 검토로 이동
          </button>
        </p>
      )}
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
                <span className="stage-time timecode">
                  {timecode(nowMs, longForm)} / {timecode(durationMs, longForm)}
                </span>
              </div>
              <div className="card-body card-screen">
                <VideoPlayer
                  ref={player}
                  sessionId={s.id}
                  onTime={(ms) => setNowMs((prev) => (Math.abs(prev - ms) >= 250 ? ms : prev))}
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
                <span className="stage-time timecode">{timecode(nowMs, longForm)}</span>
              )}
            </div>
            <div className="card-body">
              <ChatList
                chats={playhead.data?.chats ?? []}
                currentMs={nowMs}
                follow={!s.canPlay || follow}
                longForm={longForm}
                label={s.canPlay ? "재생 위치 주변 채팅" : "고른 위치 주변 채팅"}
              />
            </div>
            {!s.canPlay && (
              <p className="card-foot">영상이 없는 작업입니다. 타임라인을 누르거나 후보를 고르면 그 시각 앞뒤 채팅을 보여 줍니다.</p>
            )}
          </div>
        </div>
        {showTimeline && (
          <div className="stage-timeline card">
            <div className="card-head">
              <h2 id="timeline-title" className="stage-title">
                반응 타임라인
              </h2>
              <span className="stage-time timecode">
                {timecode(nowMs, longForm)} / {timecode(durationMs, longForm)}
              </span>
            </div>
            <div className="card-body">
              <Timeline
                durationMs={durationMs}
                windows={a?.windows ?? []}
                candidates={a?.candidates ?? []}
                extraMarkers={extraMarkers}
                selectedId={selectedId?.startsWith("rule:") ? selectedId.slice(5) : selectedId}
                onSelect={(markerId) => {
                  const itemId = markerId.includes(":") ? markerId : "rule:" + markerId;
                  const it = items.find((i) => i.id === itemId);
                  if (s.canPlay && it) seek(it.clipStartMs, itemId);
                  else select(itemId);
                }}
                playheadMs={nowMs}
                onScrub={scrub}
                scrubLabel={s.canPlay ? "재생 위치" : "채팅 볼 위치"}
              />
            </div>
          </div>
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
                  후보 <span className="tab-count">{items.length}</span>
                </>
              ),
              content: candidatesPanel,
            },
            ...tools,
          ]}
        />
      </aside>
    </>
  );
}

function CandidateContext({ analysisId, c, longForm }: { analysisId: string; c: Candidate; longForm: boolean }) {
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
      <ChatList chats={all} longForm={longForm} label="편집 구간 채팅" />
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

function Files({ sessionId, analysisId, timeline, updatedAt }: { sessionId: string; analysisId?: string; timeline?: UnifiedTimeline; updatedAt: string }) {
  const files = useQuery({
    queryKey: ["files", sessionId, analysisId, timeline?.captureId, timeline?.aiRunId, updatedAt],
    queryFn: () => api.files(sessionId),
  });
  const download = useMutation({ mutationFn: (f: ExportFile) => api.download(sessionId, f.path) });
  // Top-level chat files and the newest analysis, timeline, comment capture and AI run.
  const folders: [string | undefined, string][] = [
    ["timeline/", "통합"],
    [analysisId && analysisId + "/", "분석"],
    [timeline?.captureId && `comments/${timeline.captureId}/`, "댓글"],
    [timeline?.aiRunId && `ai/${timeline.aiRunId}/`, "AI"],
  ];
  // Chat files first, then each folder in the order above.
  const groupOf = (f: ExportFile) =>
    f.path.includes("/") ? folders.findIndex(([prefix]) => prefix && f.path.startsWith(prefix)) + 1 : 0;
  const labelOf = (f: ExportFile) => {
    if (!f.path.includes("/")) return f.path;
    const hit = folders.find(([prefix]) => prefix && f.path.startsWith(prefix));
    return hit && !f.path.includes("/markers/") ? `${hit[1]} ${f.path.split("/").pop()}` : null;
  };
  const shown = (files.data ?? [])
    .filter((f) => labelOf(f) !== null)
    .sort((x, y) => groupOf(x) - groupOf(y) || x.path.localeCompare(y.path));
  return (
    <section className="panel-section" aria-labelledby="files-title">
      <h2 id="files-title" className="section-title">
        내보내기
      </h2>
      {shown.length === 0 ? (
        <p className="empty">아직 만든 파일이 없습니다.</p>
      ) : (
        <ul className="files">
          {shown.map((f) => (
            <li key={f.path}>
              <button type="button" className="link-button" onClick={() => download.mutate(f)}>
                {labelOf(f)}
              </button>{" "}
              <span className="file-size">{formatSize(f.size)}</span>
            </li>
          ))}
        </ul>
      )}
      <p className="field-help">후보별 앞뒤 채팅(markers 폴더)은 데이터 폴더의 exports에서 볼 수 있습니다.</p>
      {download.isError && (
        <p className="error" role="alert">
          {download.error.message}
        </p>
      )}
    </section>
  );
}

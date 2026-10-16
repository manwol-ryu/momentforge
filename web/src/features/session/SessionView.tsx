import { useEffect, useMemo, useRef, useState } from "react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Analysis, Candidate, ExportFile, Session, TimelineItem, TimelineSource, UnifiedTimeline } from "../../api/types";
import { useAppState } from "../../shared/appState";
import { ChatList } from "../../shared/chat/ChatList";
import { formatSize, timecode } from "../../shared/format";
import { JobPanel } from "../../shared/job/JobPanel";
import { Link, navigate } from "../../shared/navigation";
import { VideoPlayer, type PlayerHandle } from "../../shared/player/VideoPlayer";
import { Timeline } from "../../shared/timeline/Timeline";
import { useJob } from "../../shared/useJob";
import { AIPanel } from "./AIPanel";
import { CommentsPanel } from "./CommentsPanel";
import { sourceLabel, UnifiedList } from "./UnifiedList";

const stateText: Partial<Record<Session["state"], string>> = {
  collecting: "수집 중",
  paused: "일시정지",
  finished: "수집 완료",
  comments_only: "댓글만 수집",
};

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
  const job = useJob();
  const isLive = session.data?.kind === "live";
  useEffect(() => {
    if (isLive) navigate(`/live/${id}`, { replace: true });
  }, [isLive, id]);
  const ready = session.isSuccess && (!analysisId || analysis.isSuccess) && (!isVOD || timeline.isSuccess);
  useAppState(ready ? "ready" : session.isError || analysis.isError || timeline.isError ? "error" : "loading");

  const s = session.data;
  const jobHere = job.data?.id && job.data.sessionId === id ? job.data : undefined;
  const busy = job.data?.busy ?? false;
  const hasItems = (timeline.data?.items.length ?? 0) > 0;
  return (
    <main className="page">
      <p className="back">
        <Link href="/">처음으로</Link>
      </p>
      {session.isError && (
        <p className="error" role="alert">
          {session.error.message}
        </p>
      )}
      {s && (
        <>
          <header>
            <h1 className="page-title">{s.title}</h1>
            <p className="page-lede">
              {s.isDemo ? "실제 방송이 아닌 예시 채팅" : `${s.platformName} ${stateText[s.state] ?? s.state}`}, 채팅 {s.chatCount.toLocaleString("ko-KR")}건, 길이{" "}
              <span className="timecode">{timecode(s.durationMs, s.durationMs >= 3_600_000)}</span>
              {s.watchUrl && (
                <>
                  {" "}
                  <a href={s.watchUrl} target="_blank" rel="noreferrer">
                    {s.platformName}에서 열기
                  </a>
                </>
              )}
            </p>
          </header>
          {s.state !== "finished" && <ResumeNotice s={s} busy={busy} />}
          {jobHere && <JobPanel job={jobHere} showResultLink={false} />}
          {analysis.isError && (
            <p className="error section" role="alert">
              {analysis.error.message}
            </p>
          )}
          {analysis.data || hasItems ? (
            <Review s={s} a={analysis.data} tl={timeline.data} busy={busy} aiSelection={aiSelection} onToggleAI={toggleAI} />
          ) : (
            !analysisId &&
            s.state !== "comments_only" && <p className="empty section">아직 분석 결과가 없습니다. 수집을 마치면 편집 후보를 찾습니다.</p>
          )}
          {isVOD && <CommentsPanel s={s} busy={busy} />}
          {isVOD && (analysis.data || hasItems) && <AIPanel s={s} selection={[...aiSelection]} />}
          <Files sessionId={s.id} analysisId={analysisId} timeline={timeline.data} updatedAt={s.updatedAt} />
        </>
      )}
    </main>
  );
}

function ResumeNotice({ s, busy }: { s: Session; busy: boolean }) {
  const resume = useMutation({ mutationFn: () => api.resume(s.id, false) });
  if (s.state === "collecting" && busy) return null;
  const commentsOnly = s.state === "comments_only";
  return (
    <div className="notice section">
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
}

function Review({ s, a, tl, busy, aiSelection, onToggleAI }: ReviewProps) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [filter, setFilter] = useState<SourceFilter>("all");
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
  const select = (itemId: string) => setSelectedId((cur) => (cur === itemId ? null : itemId));
  const seek = (ms: number, itemId: string) => {
    setSelectedId(itemId);
    player.current?.seek(ms);
  };

  // Chat near the playhead, fetched per 10-second step of playback.
  const step = Math.floor(nowMs / 10_000) * 10_000;
  const playhead = useQuery({
    queryKey: ["chatsAt", s.id, a?.id, step],
    queryFn: () => api.chatsAt(s.id, step + 5_000, a?.id),
    enabled: s.canPlay && s.chatCount > 0,
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

  return (
    <>
      {s.canPlay && (
        <section className="section review-media" aria-labelledby="player-title">
          <h2 id="player-title" className="section-title">
            영상과 채팅
          </h2>
          <div className="media-grid">
            <VideoPlayer
              ref={player}
              sessionId={s.id}
              onTime={(ms) => setNowMs((prev) => (Math.abs(prev - ms) >= 250 ? ms : prev))}
            />
            <div className="media-chat">
              <label className="check">
                <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} />
                재생 시간에 맞춰 채팅 따라가기
              </label>
              <ChatList
                chats={playhead.data?.chats ?? []}
                currentMs={nowMs}
                follow={follow}
                longForm={longForm}
                label="재생 위치 주변 채팅"
              />
            </div>
          </div>
        </section>
      )}

      <section className="section" aria-labelledby="timeline-title">
        <h2 id="timeline-title" className="section-title">
          반응 타임라인
        </h2>
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
        />
      </section>

      <section className="section" aria-labelledby="candidates-title">
        <div className="section-head">
          <h2 id="candidates-title" className="section-title">
            편집 후보 {shown.length}개
          </h2>
          {sources.length > 1 && (
            <label className="field-inline">
              <span className="visually-hidden">후보 출처</span>
              <select value={filter} onChange={(e) => setFilter(e.target.value as SourceFilter)} aria-label="후보 출처">
                <option value="all">모든 출처</option>
                {sources.map((src) => (
                  <option key={src} value={src}>
                    {sourceLabel[src]}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        {shown.length === 0 ? (
          <p className="empty">반응이 몰린 구간을 찾지 못했습니다. 더 작은 반응까지 보려면 '항상 가장 민감하게'로 다시 분석하세요.</p>
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
    <section className="section" aria-labelledby="files-title">
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

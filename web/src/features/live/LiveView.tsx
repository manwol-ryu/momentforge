import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Candidate, ExportFile, LiveState, LiveStatus, TimelineItem, TimelineSource } from "../../api/types";
import { AIComparePanel } from "../../shared/ai/AIComparePanel";
import { AIPanel } from "../../shared/ai/AIPanel";
import { useAppState } from "../../shared/appState";
import { sourceLabel, UnifiedList } from "../../shared/candidates/UnifiedList";
import { ChatList } from "../../shared/chat/ChatList";
import { formatSize, timecode } from "../../shared/format";
import { RollingNumber } from "../../shared/motion/RollingNumber";
import { Segmented } from "../../shared/motion/Segmented";
import { Stats } from "../../shared/motion/Stats";
import { Tabs } from "../../shared/motion/Tabs";
import { useDocumentTitle } from "../../shared/navigation";
import { VideoPlayer } from "../../shared/player/VideoPlayer";
import { liveStateText } from "../../shared/sessionText";
import { WorkbenchSkeleton } from "../../shared/skeleton/WorkbenchSkeleton";
import { Timeline } from "../../shared/timeline/Timeline";
import { LiveChatLog } from "./LiveChatLog";
import { useLiveFeed } from "./useLiveFeed";

const viewerText = { active: "적용 중", warming_up: "학습 중", missing: "자료 부족", "": "준비 중" } as const;

type PanelTab = "candidates" | "ai" | "status" | "files";

type SourceFilter = "all" | TimelineSource;

/** A candidate that stopped growing; only these can be sent for an AI review (REDESIGN 15). */
const settled = (c: Candidate) => c.state === "confirmed" || c.state === "partial";

/**
 * A LIVE capture as a workbench: state and controls on top, the broadcast,
 * the incoming chat and the reaction timeline held in view, and candidates,
 * AI review, capture state and files in tabs beside them.
 */
export function LiveView({ id }: { id: string }) {
  const status = useQuery({ queryKey: ["live", id], queryFn: () => api.liveStatus(id) });
  const feed = useLiveFeed(id);
  const [tab, setTab] = useState<PanelTab>("candidates");
  // The summary counts every source, like the 후보 tab (the same cached timeline query the workspace reads).
  const timeline = useQuery({ queryKey: ["timeline", id], queryFn: () => api.timeline(id) });
  const itemCount = feed.candidates.length + (timeline.data?.items ?? []).filter((i) => i.source === "ai").length;
  useAppState(status.isSuccess && feed.loaded ? "ready" : status.isError ? "error" : "loading");
  const v = status.data;
  useDocumentTitle(v?.session.title);

  if (status.isError) {
    return (
      <div className="page">
        <p className="error" role="alert">
          {status.error.message}
        </p>
      </div>
    );
  }
  if (!v) return <WorkbenchSkeleton />;
  return (
    <div className="ws ws-live">
      <header className="ws-head">
        <div className="ws-heading">
          <h1 className="ws-title">{v.session.title}</h1>
          <p className="ws-meta">
            {v.session.platformName} 생방송,{" "}
            <span className="live-state" data-state={v.session.state}>
              {liveStateText[v.session.state as LiveState] ?? v.session.state}
            </span>
          </p>
        </div>
        <Stats
          items={[
            { label: "채팅", value: <><RollingNumber value={v.session.chatCount} />건</> },
            { label: "편집 후보", value: <><RollingNumber value={itemCount} />개</> },
            { label: "시청자", value: v.viewerCount !== null ? <><RollingNumber value={v.viewerCount} />명</> : "-" },
            {
              label: "분석한 위치",
              value: <span className="timecode">{timecode(v.watermarkMs, v.watermarkMs >= 3_600_000)}</span>,
            },
          ]}
        />
        <Controls v={v} />
      </header>
      <Workspace v={v} feed={feed} tab={tab} onTab={setTab} />
    </div>
  );
}

function Controls({ v }: { v: LiveStatus }) {
  const queryClient = useQueryClient();
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["live", v.session.id] });
  const stop = useMutation({ mutationFn: () => api.stopLive(v.session.id), onSuccess: refresh });
  const resume = useMutation({ mutationFn: () => api.resumeLive(v.session.id), onSuccess: refresh });
  const save = useMutation({
    mutationFn: () => api.liveCheckpoint(v.session.id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["liveFiles", v.session.id] }),
  });
  // Stopping or resuming saves again, so an earlier "saved" notice would be stale.
  const resetSave = save.reset;
  useEffect(() => resetSave(), [v.running, resetSave]);
  const message = v.session.errorMessage ?? (v.running ? undefined : v.closedReason);
  const failed = stop.error ?? resume.error ?? save.error;
  return (
    <>
      <p className="ws-action actions">
        {v.running ? (
          <button type="button" className="button" onClick={() => stop.mutate()} disabled={stop.isPending || v.session.state === "stopping"}>
            수집 중단
          </button>
        ) : (
          v.session.canResume && (
            <button type="button" className="button" onClick={() => resume.mutate()} disabled={resume.isPending}>
              이어 기록
            </button>
          )
        )}
        <button type="button" className="button button-quiet" onClick={() => save.mutate()} disabled={save.isPending}>
          현재까지 저장
        </button>
        {v.session.watchUrl && (
          <a className="button button-quiet" href={v.session.watchUrl} target="_blank" rel="noreferrer">
            {v.session.platformName}에서 열기
          </a>
        )}
      </p>
      {(message || save.data || failed) && (
        <div className="ws-notices">
          {message && (
            <p className={v.session.state === "error" || v.session.state === "incomplete" ? "error" : "notice"} role="status">
              {message}
            </p>
          )}
          {save.data && (
            <p className="field-help" role="status">
              채팅 {save.data.eventCount.toLocaleString("ko-KR")}건과 편집 후보를 저장했습니다. 파일 탭에서 받을 수 있습니다.
            </p>
          )}
          {failed && (
            <p className="error" role="alert">
              {failed.message}
            </p>
          )}
        </div>
      )}
    </>
  );
}

interface WorkspaceProps {
  v: LiveStatus;
  feed: ReturnType<typeof useLiveFeed>;
  tab: PanelTab;
  onTab: (tab: PanelTab) => void;
}

function Workspace({ v, feed, tab, onTab }: WorkspaceProps) {
  const sid = v.session.id;
  // Timeline item IDs: "rule:<candidate>" or "ai:<recommendation>".
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const select = (itemId: string) => setSelectedId((cur) => (cur === itemId ? null : itemId));
  const [filter, setFilter] = useState<SourceFilter>("all");
  const [aiSelection, setAISelection] = useState<Set<string>>(() => new Set());
  const toggleAI = (itemId: string) =>
    setAISelection((cur) => {
      const next = new Set(cur);
      if (!next.delete(itemId)) next.add(itemId);
      return next;
    });
  // The density timeline refreshes per 10 s of analysed broadcast, not per event.
  const step = Math.floor(v.watermarkMs / 10_000);
  const density = useQuery({
    queryKey: ["liveDensity", sid, step],
    queryFn: () => api.liveDensity(sid),
    placeholderData: (prev) => prev,
  });
  // AI recommendations come from the unified timeline; the AI panel refreshes it when a review ends.
  const timeline = useQuery({ queryKey: ["timeline", sid], queryFn: () => api.timeline(sid) });
  const aiItems = useMemo(() => (timeline.data?.items ?? []).filter((i) => i.source === "ai"), [timeline.data]);
  const candidates = useMemo(() => new Map(feed.candidates.map((c) => [c.id, c])), [feed.candidates]);
  const items: TimelineItem[] = useMemo(
    () =>
      [
        ...feed.candidates.map((c) => ({
          id: "rule:" + c.id, source: "rule" as const, originId: c.id, anchorMs: c.clipStartMs,
          clipStartMs: c.clipStartMs, clipEndMs: c.clipEndMs, title: c.reasons.join(" · "),
        })),
        ...aiItems,
      ].sort((x, y) => x.anchorMs - y.anchorMs),
    [feed.candidates, aiItems],
  );
  const sources = [...new Set(items.map((i) => i.source))];
  const shown = filter === "all" ? items : items.filter((i) => i.source === filter);
  const durationMs = Math.max(
    density.data?.endMs ?? 0,
    v.watermarkMs,
    ...feed.candidates.map((c) => c.clipEndMs),
    ...aiItems.map((i) => i.clipEndMs),
    1,
  );
  const longForm = durationMs >= 3_600_000;
  const basis = v.timeBasis === "broadcast_elapsed" ? "방송 시작 후" : "수집 시작 후";
  const aiMarkers = aiItems.map((i, n) => ({
    id: i.id, ms: i.anchorMs, kind: "ai" as const, label: `${sourceLabel.ai} ${n + 1}, ${timecode(i.anchorMs, longForm)}`,
  }));

  return (
    <>
      <section className="ws-stage" aria-label={v.session.canPlay ? "영상, 실시간 채팅, 타임라인" : "실시간 채팅과 타임라인"}>
        <div className="stage-media" data-video={v.session.canPlay || undefined}>
          {v.session.canPlay && <LiveVideo v={v} />}
          <div className="stage-chat card">
            <div className="card-head">
              <h2 id="live-chat-title" className="stage-title">
                실시간 채팅
              </h2>
              <span className="stage-time">새 채팅이 아래에 쌓입니다</span>
            </div>
            <div className="card-body">
              <LiveChatLog chats={feed.chats} longForm={longForm} />
            </div>
            <p className="card-foot">
              &lsquo;늦게 받음&rsquo;은 연결 전에 보냈거나 분석이 지난 뒤 도착한 채팅입니다. 저장하지만 확정한 후보 계산에는 넣지 않습니다.
            </p>
          </div>
        </div>
        <div className="stage-timeline card">
          <div className="card-head">
            <h2 id="live-timeline-title" className="stage-title">
              반응 타임라인
            </h2>
            <span className="stage-time">
              {basis} 경과 시간, 분석 <span className="timecode">{timecode(v.watermarkMs, longForm)}</span>까지
            </span>
          </div>
          <div className="card-body">
            <Timeline
              durationMs={durationMs}
              windows={density.data?.windows ?? []}
              stepMs={density.data?.bucketMs ?? 5000}
              windowMs={density.data?.bucketMs ?? 5000}
              gaps={v.gaps}
              candidates={feed.candidates}
              extraMarkers={aiMarkers}
              selectedId={selectedId?.startsWith("rule:") ? selectedId.slice(5) : selectedId}
              onSelect={(markerId) => select(markerId.includes(":") ? markerId : "rule:" + markerId)}
            />
          </div>
          {v.gaps.length > 0 && <p className="card-foot">빗금은 채팅을 받지 못한 구간입니다.</p>}
        </div>
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
              content: (
                <>
                  <section className="panel-section" aria-labelledby="live-candidates-title">
                    <div className="section-head">
                      <h2 id="live-candidates-title" className="section-title">
                        편집 후보 <RollingNumber value={shown.length} />개
                      </h2>
                      {sources.length > 1 && (
                        <Segmented
                          name="live-candidate-source"
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
                        {v.running
                          ? "아직 반응이 몰린 구간이 없습니다. 반응이 끝나고 뒤 채팅까지 받으면 여기에 나타납니다."
                          : "반응이 몰린 구간을 찾지 못했습니다."}
                      </p>
                    ) : (
                      <UnifiedList
                        items={shown}
                        candidates={candidates}
                        selectedId={selectedId}
                        onSelect={select}
                        longForm={longForm}
                        canPlay={false}
                        platformName={v.session.platformName}
                        aiSelection={aiSelection}
                        onToggleAI={toggleAI}
                        canPick={(it) => {
                          const c = candidates.get(it.originId);
                          return !!c && settled(c);
                        }}
                        renderRuleDetail={(c) => <CandidateContext sessionId={sid} c={c} longForm={longForm} />}
                      />
                    )}
                    {feed.error && (
                      <p className="error" role="alert">
                        {feed.error.message}
                      </p>
                    )}
                  </section>
                  {aiSelection.size > 0 && (
                    <p className="pick-bar">
                      <span>AI 검토에 고른 후보 {aiSelection.size}개</span>
                      <button type="button" className="link-button" onClick={() => onTab("ai")}>
                        AI 검토로 이동
                      </button>
                    </p>
                  )}
                </>
              ),
            },
            {
              id: "ai",
              label: "AI 검토",
              content:
                v.session.chatCount > 0 ? (
                  <>
                    <AIPanel s={v.session} selection={[...aiSelection]} liveEndMs={v.watermarkMs} />
                    <AIComparePanel s={v.session} />
                  </>
                ) : (
                  <p className="empty">채팅을 받으면 고른 후보나 지정한 구간을 AI로 검토할 수 있습니다.</p>
                ),
            },
            { id: "status", label: "수집 상태", content: <Facts v={v} /> },
            { id: "files", label: "파일", content: <Files v={v} aiRunId={timeline.data?.aiRunId} /> },
          ]}
        />
      </aside>
    </>
  );
}

/**
 * The broadcast as the platform streams it now. It plays only while the
 * capture runs: an old capture would otherwise show whatever the channel
 * broadcasts today, and nothing loads until play is pressed.
 */
function LiveVideo({ v }: { v: LiveStatus }) {
  const elsewhere = v.session.watchUrl ? `위의 ‘${v.session.platformName}에서 열기’로 볼 수 있습니다.` : undefined;
  return (
    <div className="stage-video card">
      <div className="card-head">
        <h2 className="stage-title">영상</h2>
        <span className="stage-time">{v.running ? "지금 방송" : "꺼짐"}</span>
      </div>
      <div className="card-body card-screen">
        {v.running ? (
          <VideoPlayer sessionId={v.session.id} live fallback={elsewhere} />
        ) : (
          <div className="player-off">
            <p className="player-note">
              생방송 영상은 수집하는 동안에만 보여 줍니다.{v.session.canResume ? " 이어 기록하면 다시 불러옵니다." : ""}
            </p>
          </div>
        )}
      </div>
    </div>
  );
}

function CandidateContext({ sessionId, c, longForm }: { sessionId: string; c: Candidate; longForm: boolean }) {
  const chats = useInfiniteQuery({
    // The clip range grows while a candidate is still forming.
    queryKey: ["liveCandidateChats", sessionId, c.id, c.state, c.clipEndMs],
    queryFn: ({ pageParam }) => api.liveCandidateChats(sessionId, c.id, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => last.nextOffset ?? undefined,
  });
  const all = chats.data?.pages.flatMap((p) => p.chats) ?? [];
  const total = chats.data?.pages[0]?.total ?? 0;
  return (
    <div className="context">
      <p className="evidence-title">편집 구간 채팅 {total.toLocaleString("ko-KR")}건 (* 판정 근거)</p>
      <ChatList chats={all} longForm={longForm} label="편집 구간 채팅" />
      {chats.hasNextPage && (
        <button type="button" className="link-button" onClick={() => chats.fetchNextPage()} disabled={chats.isFetchingNextPage}>
          채팅 더 보기
        </button>
      )}
    </div>
  );
}

function Facts({ v }: { v: LiveStatus }) {
  const longForm = v.watermarkMs >= 3_600_000;
  const gapMs = v.gaps.reduce((sum, g) => sum + g.endMs - g.startMs, 0);
  return (
    <section className="panel-section" aria-labelledby="live-facts-title">
      <h2 id="live-facts-title" className="section-title">
        수집 상태
      </h2>
      <dl className="facts">
        <dt>시간 기준</dt>
        <dd>{v.timeBasis === "broadcast_elapsed" ? "방송 시작 후 경과" : "수집 시작 후 경과 (방송 시작 시각을 받지 못함)"}</dd>
        <dt>연결</dt>
        <dd>{v.generation > 1 ? `연결 복구 ${v.generation - 1}회` : v.generation === 1 ? "처음 연결 유지" : "연결 전"}</dd>
        <dt>시청자</dt>
        <dd>
          {v.viewerCount !== null ? (
            <>
              <RollingNumber value={v.viewerCount} />명
            </>
          ) : (
            "자료 없음"
          )}
          , 시청자 수 보정{" "}
          {v.viewerAdaptive ? viewerText[v.viewerStatus] : "끔"}
        </dd>
        <dt>못 받은 구간</dt>
        <dd>
          {v.gaps.length > 0 ? (
            <>
              {v.gaps.length}개, 합계 <span className="timecode">{timecode(gapMs, longForm)}</span>
            </>
          ) : (
            "없음"
          )}
        </dd>
        <dt>늦게 받은 채팅</dt>
        <dd>{v.lateCount.toLocaleString("ko-KR")}건 (저장만 하고 확정 후보 계산에서 뺌)</dd>
        <dt>다시 받은 채팅</dt>
        <dd>{v.duplicateCount.toLocaleString("ko-KR")}건 (재연결 때 다시 온 채팅이라 버림)</dd>
        <dt>판정 시작 기준</dt>
        <dd>{gateText(v)}</dd>
      </dl>
    </section>
  );
}

function gateText(v: LiveStatus): string {
  if (v.sensitive) return "10초에 6건 (항상 가장 민감하게)";
  if (!v.adaptiveGate) return "10초에 15건 (고정 기준으로 시작한 기록)";
  const w = v.lastWindow;
  if (!w?.gate) return "채널을 지켜보는 중 (처음 3분)";
  return `10초에 채팅 ${w.gate}건(최근 2시간 평소의 3.75배) 또는 채팅한 사람 ${w.spikeGate ?? "-"}명(최근 15분 평소의 5배)`;
}

const fileLabel: Record<string, string> = {
  "chat.received.txt": "받은 순서대로 채팅 TXT",
  "chat.txt": "시간순 채팅 TXT",
  "timeline.txt": "편집 후보 TXT",
  "timeline.csv": "편집 후보 CSV",
  "manifest.json": "수집 상태 JSON",
};

const aiFileLabel: Record<string, string> = {
  "recommendations.txt": "AI 추천 TXT",
  "recommendations.json": "AI 추천 JSON",
  "manifest.json": "AI 검토 기록 JSON",
};

function Files({ v, aiRunId }: { v: LiveStatus; aiRunId?: string }) {
  const files = useQuery({
    queryKey: ["liveFiles", v.session.id, v.session.state, aiRunId],
    queryFn: () => api.files(v.session.id),
  });
  const download = useMutation({ mutationFn: (f: ExportFile) => api.download(v.session.id, f.path) });
  const all = files.data ?? [];
  // Checkpoint folders are named by time, so the last one is the newest.
  const latest = all
    .map((f) => /^checkpoints\/([^/]+)\//.exec(f.path)?.[1])
    .filter((x): x is string => !!x)
    .sort()
    .pop();
  const name = (f: ExportFile) => f.path.split("/").pop()!;
  // The capture's own files (top level and the newest checkpoint), then the newest AI review's.
  const isAI = (f: ExportFile) => !!aiRunId && f.path === `ai/${aiRunId}/${name(f)}`;
  const labelOf = (f: ExportFile) =>
    isAI(f) ? aiFileLabel[name(f)]
    : !f.path.includes("/") || f.path === `checkpoints/${latest}/${name(f)}` ? fileLabel[name(f)]
    : undefined;
  const order = [...Object.keys(fileLabel), ...Object.keys(aiFileLabel).map((n) => "ai/" + n)];
  const rank = (f: ExportFile) => order.indexOf(isAI(f) ? "ai/" + name(f) : name(f));
  const shown = all.filter((f) => labelOf(f)).sort((a, b) => rank(a) - rank(b));
  return (
    <section className="panel-section" aria-labelledby="live-files-title">
      <h2 id="live-files-title" className="section-title">
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
      <p className="field-help">
        받은 순서 TXT는 수집하는 동안 계속 늘어납니다. 시간순 채팅과 편집 후보는 &lsquo;현재까지 저장&rsquo;을 누르거나 수집이 끝나면
        만들어집니다{latest ? ` (마지막 저장 ${checkpointTime(latest)})` : ""}.
      </p>
      {download.isError && (
        <p className="error" role="alert">
          {download.error.message}
        </p>
      )}
    </section>
  );
}

/** Checkpoint IDs start with their UTC time, "20261016-153012-…"; shown in local time. */
function checkpointTime(id: string): string {
  const m = /^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})/.exec(id);
  if (!m) return id;
  const [y, mo, d, h, mi, s] = m.slice(1).map(Number) as [number, number, number, number, number, number];
  return new Date(Date.UTC(y, mo - 1, d, h, mi, s)).toLocaleString("ko-KR", { dateStyle: "short", timeStyle: "medium" });
}

import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Candidate, CutBoundaryName, ExportFile, LiveState, LiveStatus, TimelineItem, TimelineSource } from "../../api/types";
import { AIReviewBar } from "../../shared/ai/AIReviewBar";
import { useAIEvents } from "../../shared/ai/useAIEvents";
import { useAppState } from "../../shared/appState";
import { foldAI, type FoldedItems } from "../../shared/candidates/foldAI";
import { candidateNumbers } from "../../shared/candidates/strength";
import { sourceLabel, UnifiedList } from "../../shared/candidates/UnifiedList";
import { CutRequest } from "../../shared/candidates/CutRequest";
import { useCutView } from "../../shared/candidates/useCutView";
import { ChatList } from "../../shared/chat/ChatList";
import { ConfirmAction } from "../../shared/confirm/ConfirmAction";
import { aiGroup, cutGroups, entriesIn, ExportFiles, olderCutFolders, transcriptGroup, type FileGroup } from "../../shared/files/ExportFiles";
import { timecode } from "../../shared/format";
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

type PanelTab = "candidates" | "status" | "files";

type SourceFilter = "all" | TimelineSource;

/** How much of the timeline is drawn: from where the chat was first received, or the whole broadcast. */
type TimelineView = "captured" | "all";

/**
 * A candidate that can go to AI: judged (or final) in a capture with tiers,
 * else one that stopped growing (REDESIGN 6.2.3, 15).
 */
const settled = (c: Candidate) => (c.tier ? c.tier !== "early" : c.state === "confirmed" || c.state === "partial");

const NO_ITEMS: TimelineItem[] = [];

/**
 * A LIVE capture as a workbench: state and controls on top, the broadcast,
 * the incoming chat and the reaction timeline held in view, and candidates
 * (with their AI review), capture state and files in tabs beside them.
 */
export function LiveView({ id }: { id: string }) {
  const queryClient = useQueryClient();
  const status = useQuery({ queryKey: ["live", id], queryFn: () => api.liveStatus(id) });
  const feed = useLiveFeed(id);
  const [tab, setTab] = useState<PanelTab>("candidates");
  // AI recommendations come from the unified timeline; the AI panel refreshes it when a review ends.
  const timeline = useQuery({ queryKey: ["timeline", id], queryFn: () => api.timeline(id) });
  const aiItems = useMemo(() => (timeline.data?.items ?? NO_ITEMS).filter((i) => i.source === "ai"), [timeline.data]);
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
  // The summary, the tab and the list count the same rows (AI explanations sit under their candidates).
  const folded = useMemo(() => foldAI(items), [items]);
  // AI runs (automatic, or asked for) refresh what they change when they end.
  useAIEvents(id);
  const showTab = (t: PanelTab) => {
    setTab(t);
    // The received-order file grows while capturing: its size is asked again whenever the files are looked at.
    if (t === "files") void queryClient.invalidateQueries({ queryKey: ["liveFiles", id] });
  };
  useAppState(status.isSuccess && feed.loaded ? "ready" : status.isError || (!feed.loaded && feed.error) ? "error" : "loading");
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
            { label: "편집 후보", value: <><RollingNumber value={folded.rows.length} />개</> },
            { label: "시청자", value: v.viewerCount !== null ? <><RollingNumber value={v.viewerCount} />명</> : "-" },
            {
              label: "분석한 위치",
              value: <span className="timecode">{timecode(v.watermarkMs, v.watermarkMs >= 3_600_000)}</span>,
            },
          ]}
        />
        <Controls v={v} feedError={feed.loaded ? feed.error : null} onRetry={feed.retry} />
      </header>
      <Workspace
        v={v}
        feed={feed}
        items={items}
        aiItems={aiItems}
        folded={folded}
        aiRunId={timeline.data?.aiRunId}
        tab={tab}
        onTab={showTab}
      />
    </div>
  );
}

interface ControlsProps {
  v: LiveStatus;
  /** A catch-up that failed after the first load (the first load's failure shows in the chat card). */
  feedError: Error | null;
  onRetry: () => void;
}

function Controls({ v, feedError, onRetry }: ControlsProps) {
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
      <div className="ws-action actions">
        {v.running ? (
          <ConfirmAction
            label="수집 중단"
            question="멈춘 동안의 채팅은 받을 수 없습니다. 이어 기록하면 빈 구간으로 남습니다."
            keepLabel="계속 수집"
            onConfirm={() => stop.mutate()}
            disabled={stop.isPending || v.session.state === "stopping"}
            className="button"
          />
        ) : (
          v.session.canResume && (
            <button type="button" className="button" onClick={() => resume.mutate()} disabled={resume.isPending}>
              이어 기록
            </button>
          )
        )}
        {/* A finished capture has already saved everything; saving is for a capture still running. */}
        {v.running && (
          <button type="button" className="button button-quiet" onClick={() => save.mutate()} disabled={save.isPending}>
            현재까지 저장
          </button>
        )}
        {v.session.watchUrl && (
          <a className="button button-quiet" href={v.session.watchUrl} target="_blank" rel="noreferrer">
            {v.session.platformName}에서 열기
          </a>
        )}
      </div>
      {(message || save.data || failed || feedError) && (
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
          {feedError && (
            <p className="error" role="alert">
              새 채팅과 후보를 받아 오지 못했습니다. {feedError.message}{" "}
              <button type="button" className="link-button" onClick={onRetry}>
                다시 받기
              </button>
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
  /** Candidates and AI results in time order, and the same folded into list rows. */
  items: TimelineItem[];
  aiItems: TimelineItem[];
  folded: FoldedItems;
  aiRunId?: string;
  tab: PanelTab;
  onTab: (tab: PanelTab) => void;
}

function Workspace({ v, feed, items, aiItems, folded, aiRunId, tab, onTab }: WorkspaceProps) {
  const sid = v.session.id;
  // Timeline item IDs: "rule:<candidate>" or "ai:<recommendation>".
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const select = (itemId: string) => setSelectedId((cur) => (cur === itemId ? null : itemId));
  // A chosen early candidate that a judgment replaced stays chosen as the judged one.
  const replacement = selectedId?.startsWith("rule:") ? feed.replacedBy.get(selectedId.slice(5)) : undefined;
  useEffect(() => {
    if (replacement !== undefined) setSelectedId(replacement ? "rule:" + replacement : null);
  }, [replacement]);
  const [filter, setFilter] = useState<SourceFilter>("all");
  const cut = useCutView(items, folded.aiFor, selectedId);
  const chooseCut = (planId: string, name: CutBoundaryName, itemId: string) => {
    cut.choose(planId, name);
    setSelectedId(itemId);
  };
  // The density timeline refreshes per 10 s of analysed broadcast, not per event.
  const step = Math.floor(v.watermarkMs / 10_000);
  const density = useQuery({
    queryKey: ["liveDensity", sid, step],
    queryFn: () => api.liveDensity(sid),
    placeholderData: (prev) => prev,
  });
  const candidates = useMemo(() => new Map(feed.candidates.map((c) => [c.id, c])), [feed.candidates]);
  const numbers = useMemo(() => candidateNumbers(feed.candidates), [feed.candidates]);
  const sources = [...new Set(folded.rows.map((i) => i.source))];
  const shown = filter === "all" ? folded.rows : folded.rows.filter((i) => i.source === filter);
  const durationMs = Math.max(
    density.data?.endMs ?? 0,
    v.watermarkMs,
    ...feed.candidates.map((c) => c.clipEndMs),
    ...aiItems.map((i) => i.clipEndMs),
    1,
  );
  const longForm = durationMs >= 3_600_000;
  const basis = v.timeBasis === "broadcast_elapsed" ? "방송 시작 후" : "수집 시작 후";
  // A capture that joined a broadcast late has no chat before it joined: the timeline can start where the chat does.
  const capturedFrom = v.gaps.find((g) => g.startMs <= 0)?.endMs ?? 0;
  const canFocus = capturedFrom > durationMs * 0.2 && capturedFrom < durationMs;
  const [view, setView] = useState<TimelineView>("captured");
  const viewStartMs = canFocus && view === "captured" ? Math.max(0, capturedFrom - (durationMs - capturedFrom) * 0.05) : 0;
  // The timeline marks what the list shows.
  const aiMarkers = filter === "all" || filter === "ai"
    ? folded.rows.filter((i) => i.source === "ai").map((i, n) => ({
        id: i.id, ms: i.anchorMs, kind: "ai" as const, label: `${sourceLabel.ai} ${n + 1}, ${timecode(i.anchorMs, longForm)}`,
      }))
    : [];
  const ruleMarkers = filter === "all" || filter === "rule" ? feed.candidates : [];

  return (
    <>
      <section className="ws-stage" aria-label={v.session.canPlay ? "영상, 실시간 채팅, 타임라인" : "실시간 채팅과 타임라인"}>
        {/* A stopped capture has nothing to play: the video card shrinks to one line and the chat takes the room. */}
        <div className="stage-media" data-video={(v.session.canPlay && v.running) || undefined} data-video-off={(v.session.canPlay && !v.running) || undefined}>
          {v.session.canPlay && <LiveVideo v={v} />}
          <div className="stage-chat card">
            <div className="card-head">
              <h2 id="live-chat-title" className="stage-title">
                실시간 채팅
              </h2>
              <span className="stage-time">{v.running ? "새 채팅이 아래에 쌓입니다" : "마지막으로 받은 채팅"}</span>
            </div>
            <div className="card-body">
              <LiveChatLog
                chats={feed.chats}
                received={feed.received}
                longForm={longForm}
                error={feed.loaded ? null : feed.error}
                onRetry={feed.retry}
              />
            </div>
            <p className="card-foot">
              &lsquo;늦게 받음&rsquo;은 연결 전에 보냈거나 분석이 지난 뒤 도착한 채팅입니다.{" "}
              {v.tiered ? "저장하고 5분 판정에는 넣지만 예비 후보 계산에는 넣지 않습니다." : "저장하지만 확정한 후보 계산에는 넣지 않습니다."}
            </p>
          </div>
        </div>
        <div className="stage-timeline card">
          <div className="card-head">
            <h2 id="live-timeline-title" className="stage-title">
              반응 타임라인
            </h2>
            <div className="timeline-tools">
              {canFocus && (
                <Segmented
                  name="live-timeline-view"
                  legend="타임라인 범위"
                  hideLegend
                  value={view}
                  onChange={setView}
                  options={[
                    { value: "captured" as TimelineView, label: "수집한 구간" },
                    { value: "all" as TimelineView, label: "방송 전체" },
                  ]}
                />
              )}
              <span className="stage-time">
                {basis} 경과 시간, 분석 <span className="timecode">{timecode(v.watermarkMs, longForm)}</span>까지
              </span>
            </div>
          </div>
          <div className="card-body">
            <Timeline
              durationMs={durationMs}
              windows={density.data?.windows ?? []}
              stepMs={density.data?.bucketMs ?? 5000}
              windowMs={density.data?.bucketMs ?? 5000}
              gaps={v.gaps}
              candidates={ruleMarkers}
              numbers={numbers}
              extraMarkers={aiMarkers}
              cutSpans={cut.spans}
              selectedId={selectedId?.startsWith("rule:") ? selectedId.slice(5) : selectedId}
              onSelect={(markerId) => select(markerId.includes(":") ? markerId : "rule:" + markerId)}
              viewStartMs={viewStartMs}
              viewEndMs={durationMs}
            />
          </div>
          {v.gaps.length > 0 && (
            <p className="card-foot">
              빗금은 채팅을 받지 못한 구간입니다.
              {canFocus && view === "captured" && ` 수집을 시작하기 전(${timecode(capturedFrom, longForm)}까지)은 아래 얇은 막대에만 보입니다.`}
            </p>
          )}
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
                  후보 <span className="tab-count">{folded.rows.length}</span>
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
                    {v.tiered && (
                      <p className="tier-legend">
                        채팅이 몰리면 <span className="candidate-tier" data-tier="early">예비</span>로 먼저 띄우고, 반응 5분 뒤
                        다시보기와 같은 기준으로 <span className="candidate-tier" data-tier="judged">5분 판정</span>, 30분 뒤{" "}
                        <span className="candidate-tier" data-tier="final">확정</span>합니다. 판정에서 빠진 예비 후보는 목록에서
                        사라집니다.
                      </p>
                    )}
                    <AIReviewBar s={v.session} hasCandidates={feed.candidates.some(settled)} busy={false} />
                    {shown.length === 0 ? (
                      <p className="empty">
                        {folded.rows.length > 0
                          ? "고른 출처에 맞는 후보가 없습니다. 위에서 모든 출처를 고르세요."
                          : v.running
                            ? v.tiered
                              ? "아직 반응이 몰린 구간이 없습니다. 채팅이 몰리면 예비 후보로 바로 나타납니다."
                              : "아직 반응이 몰린 구간이 없습니다. 반응이 끝나고 뒤 채팅까지 받으면 여기에 나타납니다."
                            : "반응이 몰린 구간을 찾지 못했습니다."}
                      </p>
                    ) : (
                      <UnifiedList
                        items={shown}
                        candidates={candidates}
                        numbers={numbers}
                        aiFor={folded.aiFor}
                        selectedId={selectedId}
                        onSelect={select}
                        longForm={longForm}
                        canPlay={false}
                        platformName={v.session.platformName}
                        cutChoice={cut.choice}
                        onCutChoice={chooseCut}
                        renderCut={(it, name, planId) => {
                          // A candidate still forming grows its range, and an early one may be dropped: analysed once settled.
                          const c = candidates.get(it.originId);
                          if (it.source === "rule" && !(c && settled(c))) return null;
                          return (
                            <CutRequest sessionId={sid} itemId={it.id} name={name} planId={planId} label={planId ? "다시 분석" : "AI 컷 분석"} />
                          );
                        }}
                        renderRuleDetail={(c) => <CandidateContext sessionId={sid} c={c} longForm={longForm} />}
                      />
                    )}
                  </section>
                </>
              ),
            },
            { id: "status", label: "수집 상태", content: <Facts v={v} /> },
            { id: "files", label: "파일", content: <Files v={v} aiRunId={aiRunId} items={aiItems} longForm={longForm} /> },
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
  // What the broadcaster said there, once a judged candidate's range was transcribed (생방송 음성).
  const speech = useQuery({
    queryKey: ["utterances", sessionId, "clip", c.clipStartMs, c.clipEndMs],
    queryFn: () => api.utterances(sessionId, c.clipStartMs, c.clipEndMs, 200),
    enabled: c.tier !== undefined && c.tier !== "early",
  });
  const lines = speech.data?.utterances ?? [];
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
      {lines.length > 0 && (
        <>
          <p className="evidence-title">편집 구간 방송 내용 {lines.length.toLocaleString("ko-KR")}줄 (이 컴퓨터에서 인식)</p>
          <ol className="chatlog transcript-lines" aria-label="편집 구간 발언">
            {lines.map((u) => (
              <li key={u.id}>
                <span className="chat-row" title={u.rawText ? `인식한 원문: ${u.rawText}` : undefined}>
                  <span className="chat-time timecode">{timecode(u.startMs, longForm)}</span>
                  <span className="chat-author" data-tone={3}>
                    {u.speaker}
                  </span>
                  <span className="chat-text">{u.text}</span>
                  {u.rawText && (
                    <span className="chat-fixed" aria-label={`AI가 다듬음. 인식한 원문: ${u.rawText}`}>
                      다듬음
                    </span>
                  )}
                </span>
              </li>
            ))}
          </ol>
        </>
      )}
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
        <dd>
          {v.lateCount.toLocaleString("ko-KR")}건{" "}
          {v.tiered ? "(예비 후보에서는 빼고 5분 판정에는 넣음)" : "(저장만 하고 확정 후보 계산에서 뺌)"}
        </dd>
        <dt>다시 받은 채팅</dt>
        <dd>{v.duplicateCount.toLocaleString("ko-KR")}건 (재연결 때 다시 온 채팅이라 버림)</dd>
        <dt>{v.tiered ? "예비 후보 기준" : "판정 시작 기준"}</dt>
        <dd>{gateText(v)}</dd>
        {v.tiered && (
          <>
            <dt>후보 판정</dt>
            <dd>반응 5분 뒤 다시보기와 같은 기준(앞뒤 5분·30분과 비교)으로 판정, 30분 뒤 또는 수집을 마칠 때 확정</dd>
          </>
        )}
        {v.running && v.audio && (
          <>
            <dt>방송 음성</dt>
            <dd>{audioText(v)}</dd>
          </>
        )}
        {v.autoNote && (
          <>
            <dt>자동 작업</dt>
            <dd className="error">{v.autoNote}</dd>
          </>
        )}
      </dl>
    </section>
  );
}

function gateText(v: LiveStatus): string {
  if (v.sensitive) return "10초에 6건 (항상 가장 민감하게)";
  if (!v.adaptiveGate) return "10초에 15건 (고정 기준으로 시작한 기록)";
  const w = v.lastWindow;
  if (!w?.gate) return "채널을 지켜보는 중 (처음 3분)";
  // Early candidates open at two thirds of the usual gate (detect.Params.EarlyGateScale).
  const [busy, spike] = v.tiered ? ["2.5", "3.35"] : ["3.75", "5"];
  return `10초에 채팅 ${w.gate}건(최근 2시간 평소의 ${busy}배) 또는 채팅한 사람 ${w.spikeGate ?? "-"}명(최근 15분 평소의 ${spike}배)`;
}

/** The capture's audio recording for candidate transcripts. */
function audioText(v: LiveStatus): string {
  switch (v.audio) {
    case "recording":
      return "받는 중 (가장 낮은 화질, 최근 25분만 이 컴퓨터에 두고 수집을 마치면 삭제). 판정한 후보 구간만 인식합니다.";
    case "ended":
      return "방송 스트림이 끝나 더 받지 않습니다.";
    case "failed":
      return v.audioNote || "방송 음성을 받지 못했습니다.";
    default:
      return "받지 않음 (설정 > 일반 설정에서 켤 수 있음)";
  }
}

const savedText: Record<string, string> = {
  "timeline.txt": "편집 후보 TXT",
  "timeline.csv": "편집 후보 CSV",
  "timeline.json": "편집 후보 JSON",
};
const savedChatText: Record<string, string> = {
  "chat.txt": "시간순 채팅 TXT",
  "chat.jsonl": "채팅 JSONL",
};

/**
 * A capture's files in groups: the confirmed cuts, the candidates and chat
 * of the newest save (a checkpoint, or the final one when the capture
 * stopped), the chat as received (growing while capturing), the newest AI
 * review, each cut analysis and the capture record.
 */
function Files({ v, aiRunId, items, longForm }: { v: LiveStatus; aiRunId?: string; items: TimelineItem[]; longForm: boolean }) {
  const files = useQuery({
    queryKey: ["liveFiles", v.session.id, v.session.state, aiRunId, items.filter((i) => i.kind === "cut").length],
    queryFn: () => api.files(v.session.id),
  });
  const all = files.data ?? [];
  // Checkpoint folders are named by time, so the last one is the newest.
  const latest = all
    .map((f: ExportFile) => /^checkpoints\/([^/]+)\//.exec(f.path)?.[1])
    .filter((x): x is string => !!x)
    .sort()
    .pop();
  const saved = latest ? `checkpoints/${latest}/` : "";
  // Replaced cut analyses are named from the saved plans, asked for only when there are any.
  const older = olderCutFolders(all, items);
  const cutPlans = useQuery({
    queryKey: ["cutPlans", v.session.id, older.join(",")],
    queryFn: () => api.cutPlans(v.session.id),
    enabled: older.length > 0,
  });
  const [confirmed, ...plans] = cutGroups(all, items, longForm, cutPlans.data?.plans);
  const transcript = useQuery({ queryKey: ["transcript", v.session.id], queryFn: () => api.transcript(v.session.id) });
  const record = entriesIn(all, saved, { "manifest.json": "수집 상태 JSON" });
  const groups: FileGroup[] = [
    confirmed!,
    { id: "candidates", title: "편집 후보", entries: latest ? entriesIn(all, saved, savedText) : [] },
    {
      id: "chat",
      title: "채팅",
      entries: [
        ...entriesIn(all, "", { "chat.received.txt": "받은 순서대로 채팅 TXT" }),
        ...(latest ? entriesIn(all, saved, savedChatText) : []),
      ],
    },
    aiGroup(all, aiRunId),
    ...plans,
    // The candidates' ranges recognised from the broadcast's audio while capturing.
    transcriptGroup(all, transcript.data?.id),
    { id: "record", title: "수집 기록", entries: record },
  ];
  return (
    <section className="panel-section" aria-labelledby="live-files-title">
      <h2 id="live-files-title" className="section-title">
        내보낸 파일
      </h2>
      {files.isError ? (
        <p className="error" role="alert">
          {files.error.message}
        </p>
      ) : (
        <ExportFiles sessionId={v.session.id} groups={groups} empty={files.isPending ? "파일 목록을 불러오는 중" : "아직 만든 파일이 없습니다."} />
      )}
      <p className="field-help">
        {v.running
          ? "받은 순서대로 채팅 TXT는 수집하는 동안 계속 늘어납니다. 시간순 채팅과 편집 후보는 ‘현재까지 저장’을 누르거나 수집이 끝나면 만들어집니다"
          : "수집을 멈출 때 시간순 채팅과 편집 후보를 마지막으로 저장했습니다"}
        {latest ? ` (마지막 저장 ${checkpointTime(latest)})` : ""}.
      </p>
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

import { useEffect, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Candidate, ExportFile, LiveState, LiveStatus } from "../../api/types";
import { useAppState } from "../../shared/appState";
import { CandidateList } from "../../shared/candidates/CandidateList";
import { ChatList } from "../../shared/chat/ChatList";
import { formatSize, timecode } from "../../shared/format";
import { PageBar, useScrolledPast } from "../../shared/motion/PageBar";
import { RollingNumber } from "../../shared/motion/RollingNumber";
import { Link } from "../../shared/navigation";
import { Timeline } from "../../shared/timeline/Timeline";
import { LiveChatLog } from "./LiveChatLog";
import { useLiveFeed } from "./useLiveFeed";

export const liveStateText: Record<LiveState, string> = {
  starting: "연결 준비 중",
  collecting: "채팅 수신 중",
  reconnecting: "재연결 중",
  stopping: "남은 자료 저장 중",
  stopped: "중단됨",
  ended: "방송 종료",
  error: "확인 필요",
  incomplete: "저장 상태 확인 필요",
};

const viewerText = { active: "적용 중", warming_up: "학습 중", missing: "자료 부족", "": "준비 중" } as const;

export function LiveView({ id }: { id: string }) {
  const status = useQuery({ queryKey: ["live", id], queryFn: () => api.liveStatus(id) });
  const feed = useLiveFeed(id);
  useAppState(status.isSuccess && feed.loaded ? "ready" : status.isError ? "error" : "loading");
  const v = status.data;
  const [header, scrolledPast] = useScrolledPast();

  return (
    <main className="page">
      <p className="back">
        <Link href="/">처음으로</Link>
      </p>
      {status.isError && (
        <p className="error" role="alert">
          {status.error.message}
        </p>
      )}
      {v && (
        <>
          <PageBar title={v.session.title} shown={scrolledPast}>
            <span className="bar-state" data-state={v.session.state}>
              {liveStateText[v.session.state as LiveState] ?? v.session.state}
            </span>
          </PageBar>
          <header ref={header}>
            <h1 className="page-title">{v.session.title}</h1>
            <p className="page-lede">
              {v.session.platformName} 생방송,{" "}
              <span className="live-state" data-state={v.session.state}>
                {liveStateText[v.session.state as LiveState] ?? v.session.state}
              </span>
              , 채팅 <RollingNumber value={v.session.chatCount} />건
              {v.session.watchUrl && (
                <>
                  {" "}
                  <a href={v.session.watchUrl} target="_blank" rel="noreferrer">
                    {v.session.platformName}에서 열기
                  </a>
                </>
              )}
            </p>
          </header>
          <Controls v={v} />
          <Workspace v={v} feed={feed} />
          <Facts v={v} />
          <Files v={v} />
        </>
      )}
    </main>
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
    <div className="live-controls section">
      {message && (
        <p className={v.session.state === "error" || v.session.state === "incomplete" ? "error" : "notice"} role="status">
          {message}
        </p>
      )}
      <p className="actions">
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
      </p>
      {save.data && (
        <p className="field-help" role="status">
          채팅 {save.data.eventCount.toLocaleString("ko-KR")}건과 편집 후보를 저장했습니다. 아래 내보내기에서 받을 수 있습니다.
        </p>
      )}
      {failed && (
        <p className="error" role="alert">
          {failed.message}
        </p>
      )}
    </div>
  );
}

function Workspace({ v, feed }: { v: LiveStatus; feed: ReturnType<typeof useLiveFeed> }) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const select = (cid: string) => setSelectedId((cur) => (cur === cid ? null : cid));
  // The density timeline refreshes per 10 s of analysed broadcast, not per event.
  const step = Math.floor(v.watermarkMs / 10_000);
  const density = useQuery({
    queryKey: ["liveDensity", v.session.id, step],
    queryFn: () => api.liveDensity(v.session.id),
    placeholderData: (prev) => prev,
  });
  const durationMs = Math.max(density.data?.endMs ?? 0, v.watermarkMs, ...feed.candidates.map((c) => c.clipEndMs), 1);
  const longForm = durationMs >= 3_600_000;
  const basis = v.timeBasis === "broadcast_elapsed" ? "방송 시작 후" : "수집 시작 후";

  return (
    <>
      <section className="section" aria-labelledby="live-timeline-title">
        <h2 id="live-timeline-title" className="section-title">
          반응 타임라인
        </h2>
        <p className="field-help">
          {basis} 경과 시간입니다. 빗금은 채팅을 받지 못한 구간이고, 분석은{" "}
          <span className="timecode">{timecode(v.watermarkMs, longForm)}</span>까지 끝났습니다.
        </p>
        <Timeline
          durationMs={durationMs}
          windows={density.data?.windows ?? []}
          stepMs={density.data?.bucketMs ?? 5000}
          windowMs={density.data?.bucketMs ?? 5000}
          gaps={v.gaps}
          candidates={feed.candidates}
          selectedId={selectedId}
          onSelect={select}
        />
      </section>

      <div className="live-grid section">
        <section aria-labelledby="live-chat-title">
          <h2 id="live-chat-title" className="section-title">
            실시간 채팅
          </h2>
          <LiveChatLog chats={feed.chats} longForm={longForm} />
          <p className="field-help">
            &lsquo;늦게 받음&rsquo;은 연결 전에 보냈거나 분석이 지난 뒤 도착한 채팅입니다. 저장은 하지만 확정한 후보 계산에는 넣지
            않습니다.
          </p>
        </section>
        <section aria-labelledby="live-candidates-title">
          <h2 id="live-candidates-title" className="section-title">
            편집 후보 <RollingNumber value={feed.candidates.length} />개
          </h2>
          <CandidateList
            candidates={feed.candidates}
            selectedId={selectedId}
            onSelect={select}
            longForm={longForm}
            emptyText={
              v.running
                ? "아직 반응이 몰린 구간이 없습니다. 반응이 끝나고 뒤 채팅까지 받으면 여기에 나타납니다."
                : "반응이 몰린 구간을 찾지 못했습니다."
            }
            renderDetail={(c) => <CandidateContext sessionId={v.session.id} c={c} longForm={longForm} />}
          />
          {feed.error && (
            <p className="error" role="alert">
              {feed.error.message}
            </p>
          )}
        </section>
      </div>
    </>
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
    <section className="section" aria-labelledby="live-facts-title">
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

function Files({ v }: { v: LiveStatus }) {
  const files = useQuery({ queryKey: ["liveFiles", v.session.id, v.session.state], queryFn: () => api.files(v.session.id) });
  const download = useMutation({ mutationFn: (f: ExportFile) => api.download(v.session.id, f.path) });
  const all = files.data ?? [];
  // Checkpoint folders are named by time, so the last one is the newest.
  const latest = all
    .map((f) => /^checkpoints\/([^/]+)\//.exec(f.path)?.[1])
    .filter((x): x is string => !!x)
    .sort()
    .pop();
  const order = Object.keys(fileLabel);
  const name = (f: ExportFile) => f.path.split("/").pop()!;
  const shown = all
    .filter((f) => name(f) in fileLabel && (!f.path.includes("/") || f.path === `checkpoints/${latest}/${name(f)}`))
    .sort((a, b) => order.indexOf(name(a)) - order.indexOf(name(b)));
  return (
    <section className="section" aria-labelledby="live-files-title">
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
                {fileLabel[name(f)]}
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

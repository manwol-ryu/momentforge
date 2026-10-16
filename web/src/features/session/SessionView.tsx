import { useEffect, useRef, useState } from "react";
import { keepPreviousData, useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Analysis, Candidate, ExportFile, Session } from "../../api/types";
import { useAppState } from "../../shared/appState";
import { CandidateList } from "../../shared/candidates/CandidateList";
import { ChatList } from "../../shared/chat/ChatList";
import { formatSize, timecode } from "../../shared/format";
import { JobPanel } from "../../shared/job/JobPanel";
import { Link, navigate } from "../../shared/navigation";
import { VideoPlayer, type PlayerHandle } from "../../shared/player/VideoPlayer";
import { Timeline } from "../../shared/timeline/Timeline";
import { useJob } from "../../shared/useJob";

const stateText: Partial<Record<Session["state"], string>> = { collecting: "수집 중", paused: "일시정지", finished: "수집 완료" };

export function SessionView({ id }: { id: string }) {
  const session = useQuery({ queryKey: ["session", id], queryFn: () => api.session(id) });
  const analysisId = session.data?.latestAnalysisId;
  const analysis = useQuery({
    queryKey: ["analysis", analysisId],
    queryFn: () => api.analysis(analysisId!),
    enabled: !!analysisId,
  });
  const job = useJob();
  const isLive = session.data?.kind === "live";
  useEffect(() => {
    if (isLive) navigate(`/live/${id}`, { replace: true });
  }, [isLive, id]);
  const ready = session.isSuccess && (!analysisId || analysis.isSuccess);
  useAppState(ready ? "ready" : session.isError || analysis.isError ? "error" : "loading");

  const s = session.data;
  const jobHere = job.data?.id && job.data.sessionId === id ? job.data : undefined;
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
              {s.isDemo ? "실제 방송이 아닌 예시 채팅" : `${s.platformName} ${stateText[s.state]}`}, 채팅 {s.chatCount.toLocaleString("ko-KR")}건, 길이{" "}
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
          {s.state !== "finished" && <ResumeNotice s={s} busy={job.data?.busy ?? false} />}
          {jobHere && <JobPanel job={jobHere} showResultLink={false} />}
          {analysis.isError && (
            <p className="error section" role="alert">
              {analysis.error.message}
            </p>
          )}
          {analysis.data ? (
            <Review s={s} a={analysis.data} busy={job.data?.busy ?? false} />
          ) : (
            !analysisId && <p className="empty section">아직 분석 결과가 없습니다. 수집을 마치면 편집 후보를 찾습니다.</p>
          )}
          <Files sessionId={s.id} analysisId={analysisId} updatedAt={s.updatedAt} />
        </>
      )}
    </main>
  );
}

function ResumeNotice({ s, busy }: { s: Session; busy: boolean }) {
  const resume = useMutation({ mutationFn: () => api.resume(s.id, false) });
  if (s.state === "collecting" && busy) return null;
  return (
    <div className="notice section">
      <p>
        {s.errorMessage ?? "수집이 끝나지 않았습니다."}{" "}
        <span className="timecode">{timecode(s.cursor, s.durationMs >= 3_600_000)}</span>까지 저장되어 있습니다.
      </p>
      {s.canResume && (
        <p className="actions">
          <button type="button" className="button" onClick={() => resume.mutate()} disabled={busy || resume.isPending}>
            이어받기
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

function Review({ s, a, busy }: { s: Session; a: Analysis; busy: boolean }) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [nowMs, setNowMs] = useState(0);
  const [follow, setFollow] = useState(true);
  const player = useRef<PlayerHandle>(null);
  const longForm = a.durationMs >= 3_600_000;
  const select = (id: string) => setSelectedId((cur) => (cur === id ? null : id));
  const seek = (c: Candidate) => {
    setSelectedId(c.id);
    player.current?.seek(c.clipStartMs);
  };

  // Chat near the playhead, fetched per 10-second step of playback.
  const step = Math.floor(nowMs / 10_000) * 10_000;
  const playhead = useQuery({
    queryKey: ["chatsAt", s.id, a.id, step],
    queryFn: () => api.chatsAt(s.id, step + 5_000, a.id),
    enabled: s.canPlay,
    placeholderData: keepPreviousData,
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
          durationMs={a.durationMs}
          windows={a.windows}
          candidates={a.candidates}
          selectedId={selectedId}
          onSelect={(cid) => {
            const c = a.candidates.find((x) => x.id === cid);
            if (s.canPlay && c) seek(c);
            else select(cid);
          }}
        />
      </section>

      <section className="section" aria-labelledby="candidates-title">
        <h2 id="candidates-title" className="section-title">
          편집 후보 {a.candidates.length}개
        </h2>
        <CandidateList
          candidates={a.candidates}
          selectedId={selectedId}
          onSelect={select}
          longForm={longForm}
          renderActions={(c) => (
            <>
              {s.canPlay && (
                <button type="button" className="link-button" onClick={() => seek(c)}>
                  영상에서 보기
                </button>
              )}
              {c.watchUrl && (
                <a href={c.watchUrl} target="_blank" rel="noreferrer">
                  {s.platformName}에서 열기
                </a>
              )}
            </>
          )}
          renderDetail={(c) => <CandidateContext analysisId={a.id} c={c} longForm={longForm} />}
        />
        <Reanalyze s={s} a={a} busy={busy} />
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

function Files({ sessionId, analysisId, updatedAt }: { sessionId: string; analysisId?: string; updatedAt: string }) {
  const files = useQuery({ queryKey: ["files", sessionId, analysisId, updatedAt], queryFn: () => api.files(sessionId) });
  const download = useMutation({ mutationFn: (f: ExportFile) => api.download(sessionId, f.path) });
  const shown = (files.data ?? []).filter((f) => !f.path.includes("/") || (analysisId && f.path.startsWith(analysisId + "/") && !f.path.includes("/markers/")));
  const label = (f: ExportFile) => (f.path.includes("/") ? "분석 " + f.path.split("/").pop() : f.path);
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
                {label(f)}
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

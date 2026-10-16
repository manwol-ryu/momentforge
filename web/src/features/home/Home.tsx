import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { LiveState, Session } from "../../api/types";
import { LiveForm } from "../live/LiveForm";
import { liveStateText } from "../live/LiveView";
import { queryState, useAppState } from "../../shared/appState";
import { useEvents, type ConnectionState } from "../../shared/events";
import { timecode } from "../../shared/format";
import { JobPanel } from "../../shared/job/JobPanel";
import { Link, navigate } from "../../shared/navigation";
import { useJob } from "../../shared/useJob";

const connectionText: Record<ConnectionState, string> = {
  connecting: "연결하는 중",
  open: "연결됨",
  closed: "연결이 끊겨 다시 연결하는 중",
};

const stateText: Partial<Record<Session["state"], string>> = {
  collecting: "수집 중",
  paused: "일시정지",
  finished: "수집 완료",
};

const liveTerminal = new Set<string>(["stopped", "ended", "error", "incomplete"]);

export function Home() {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status });
  // Chat counts change while collecting, so the list is reloaded whenever Home opens.
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: api.sessions, refetchOnMount: "always" });
  const job = useJob();
  const { state } = useEvents();
  const runningLive = sessions.data?.find((s) => s.kind === "live" && !liveTerminal.has(s.state));
  useAppState(queryState({ isSuccess: status.isSuccess && sessions.isSuccess, isError: status.isError || sessions.isError }));

  return (
    <main className="page">
      <header>
        <h1 className="page-title">MomentForge</h1>
        <p className="page-lede">방송 채팅에서 편집할 순간을 찾습니다.</p>
      </header>

      <section className="section" aria-labelledby="collect-title">
        <h2 id="collect-title" className="section-title">
          다시보기 수집
        </h2>
        <CollectForm busy={job.data?.busy ?? false} />
        {job.data?.id && <JobPanel job={job.data} />}
      </section>

      <section className="section" aria-labelledby="live-title">
        <h2 id="live-title" className="section-title">
          생방송 수집
        </h2>
        <LiveForm running={runningLive} />
      </section>

      <section className="section" aria-labelledby="sessions-title">
        <h2 id="sessions-title" className="section-title">
          저장한 작업
        </h2>
        {sessions.data && sessions.data.length > 0 ? (
          <ul className="sessions">
            {sessions.data.map((s) => (
              <SessionRow key={s.id} s={s} />
            ))}
          </ul>
        ) : (
          <p className="empty">아직 수집한 방송이 없습니다. 위에 다시보기나 생방송 주소를 넣거나 샘플 채팅으로 먼저 살펴보세요.</p>
        )}
        <p className="actions">
          <DemoButton existing={sessions.data?.find((s) => s.isDemo)} />
        </p>
      </section>

      <section className="section" aria-labelledby="app-title">
        <h2 id="app-title" className="section-title">
          앱 상태
        </h2>
        {status.isError ? (
          <p className="error" role="alert">
            {status.error.message}
          </p>
        ) : (
          <dl className="facts">
            <dt>서버 연결</dt>
            <dd>
              <span className="connection" data-state={state}>
                {connectionText[state]}
              </span>
            </dd>
            <dt>버전</dt>
            <dd>{status.data?.version ?? "확인하는 중"}</dd>
            <dt>지원 플랫폼</dt>
            <dd>{status.data?.platforms.join(", ") || "확인하는 중"}</dd>
            <dt>데이터 폴더</dt>
            <dd>{status.data?.dataDir ?? "확인하는 중"}</dd>
            <dt>저장소 형식</dt>
            <dd className="timecode">{status.data ? `스키마 ${status.data.schemaVersion}` : "확인하는 중"}</dd>
          </dl>
        )}
      </section>
    </main>
  );
}

function CollectForm({ busy }: { busy: boolean }) {
  const [url, setUrl] = useState("");
  const [sensitive, setSensitive] = useState(false);
  const collect = useMutation({ mutationFn: () => api.collect(url.trim(), sensitive) });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    collect.mutate();
  };
  return (
    <form className="collect" onSubmit={submit}>
      <label className="field">
        <span className="field-label">치지직 다시보기 주소</span>
        <input
          type="url"
          inputMode="url"
          required
          placeholder="https://chzzk.naver.com/video/영상번호"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          aria-describedby="collect-help"
        />
      </label>
      <label className="check">
        <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
        항상 가장 민감하게 (10초에 채팅 6건부터 판정합니다. 작은 반응도 찾지만 잘못 잡는 후보가 늘 수 있습니다)
      </label>
      <p id="collect-help" className="field-help">
        채팅을 모두 받은 뒤 편집 후보를 찾습니다. 판정 기준은 채널 규모와 시간대에 맞춰 최근 2시간·15분의 평소 채팅량으로 자동으로
        정합니다. 긴 방송은 몇 분 걸릴 수 있고, 중단해도 받은 데까지 저장됩니다.
      </p>
      <p className="actions">
        <button type="submit" className="button" disabled={busy || collect.isPending}>
          수집하고 편집점 찾기
        </button>
      </p>
      {collect.isError && (
        <p className="error" role="alert">
          {collect.error.message}
        </p>
      )}
    </form>
  );
}

// Opens the sample, creating it the first time.
function DemoButton({ existing }: { existing?: Session }) {
  const queryClient = useQueryClient();
  const demo = useMutation({
    mutationFn: () => api.demo(),
    onSuccess: (s) => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      navigate(`/sessions/${s.id}`);
    },
  });
  return (
    <>
      <button
        type="button"
        className="button button-quiet"
        onClick={() => (existing ? navigate(`/sessions/${existing.id}`) : demo.mutate())}
        disabled={demo.isPending}
      >
        샘플로 먼저 체험
      </button>
      {demo.isError && (
        <span className="error" role="alert">
          {" "}
          {demo.error.message}
        </span>
      )}
    </>
  );
}

function SessionRow({ s }: { s: Session }) {
  const longForm = s.durationMs >= 3_600_000;
  const live = s.kind === "live";
  const what = s.isDemo
    ? "예시 채팅"
    : live
      ? `${s.platformName} 생방송 ${liveStateText[s.state as LiveState] ?? s.state}`
      : `${s.platformName} ${stateText[s.state] ?? s.state}`;
  return (
    <li className="session-row" data-state={s.state}>
      <Link href={live ? `/live/${s.id}` : `/sessions/${s.id}`} className="session-title">
        {s.title}
      </Link>
      <span className="session-meta">
        {what}, 채팅 {s.chatCount.toLocaleString("ko-KR")}건, 길이{" "}
        <span className="timecode">{timecode(s.durationMs, longForm)}</span>
        {s.state === "paused" && s.errorMessage ? ` (${s.errorMessage})` : ""}
      </span>
    </li>
  );
}

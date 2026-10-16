import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { queryState, useAppState } from "../../shared/appState";
import { useEvents, type ConnectionState } from "../../shared/events";
import { Link } from "../../shared/navigation";

const connectionText: Record<ConnectionState, string> = {
  connecting: "연결하는 중",
  open: "연결됨",
  closed: "연결이 끊겨 다시 연결하는 중",
};

export function Home() {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status });
  const { state } = useEvents();

  useAppState(queryState(status));

  return (
    <main className="page">
      <header>
        <h1 className="page-title">MomentForge</h1>
        <p className="page-lede">방송 채팅에서 편집할 순간을 찾습니다.</p>
      </header>

      <section className="section" aria-labelledby="sessions-title">
        <h2 id="sessions-title" className="section-title">
          저장한 작업
        </h2>
        <p className="empty">
          아직 수집한 방송이 없습니다. 다시보기·라이브 주소로 채팅을 수집하는 기능은 다음 업데이트에서 이 자리에
          추가됩니다. 그 전에 예시 채팅으로 편집 후보를 찾는 과정을 볼 수 있습니다.
        </p>
        <p className="actions">
          <Link href="/demo" className="button">
            샘플로 먼저 체험
          </Link>
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
              <span className="connection" data-state={state} role="status">
                {connectionText[state]}
              </span>
            </dd>
            <dt>버전</dt>
            <dd>{status.data?.version ?? "확인하는 중"}</dd>
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

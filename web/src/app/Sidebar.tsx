import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import type { Session } from "../api/types";
import { useEvents, type ConnectionState } from "../shared/events";
import { Link } from "../shared/navigation";
import { BrandMark } from "./BrandMark";
import { SettingsIcon } from "./SettingsIcon";
import { isRunningLive, sessionHref, sessionKindText, sessionSummary } from "../shared/sessionText";

const connectionText: Record<ConnectionState, string> = {
  connecting: "연결하는 중",
  open: "연결됨",
  closed: "연결이 끊겨 다시 연결하는 중",
};

/** Saved work, newest first, with the way to start another, the settings and the app's connection. */
export function Sidebar({ id, path, onFold }: { id: string; path: string; onFold: () => void }) {
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: api.sessions });
  const status = useQuery({ queryKey: ["status"], queryFn: api.status });
  const { state } = useEvents();
  const active = /^\/(?:sessions|live)\/([0-9a-f]{32})$/.exec(path)?.[1];
  const list = sessions.data ?? [];

  return (
    <aside id={id} className="sidebar" aria-label="작업 목록">
      <div className="sidebar-head">
        <Link href="/" className="brand">
          <BrandMark />
          MomentForge
        </Link>
        <button type="button" className="icon-button sidebar-fold" aria-label="작업 목록 접기" onClick={onFold}>
          <span className="fold-icon" aria-hidden="true" />
        </button>
      </div>
      <Link href="/" className="button new-task" aria-current={path === "/" ? "page" : undefined}>
        새 작업
      </Link>

      <nav className="sidebar-nav" aria-labelledby={`${id}-title`}>
        <h2 id={`${id}-title`} className="sidebar-title">
          저장한 작업
        </h2>
        {sessions.isPending ? (
          <ul className="sessions" aria-busy="true" aria-label="저장한 작업을 불러오는 중">
            {[0, 1, 2].map((i) => (
              <li key={i} className="skeleton-row" aria-hidden="true">
                <span className="skeleton skeleton-title" />
                <span className="skeleton skeleton-line" />
              </li>
            ))}
          </ul>
        ) : sessions.isError ? (
          <p className="error" role="alert">
            {sessions.error.message}
          </p>
        ) : list.length > 0 ? (
          <ul className="sessions">
            {list.map((s) => (
              <SessionRow key={s.id} s={s} current={s.id === active} />
            ))}
          </ul>
        ) : (
          <p className="sidebar-empty">아직 작업이 없습니다. 새 작업에서 방송 주소를 넣거나 샘플을 열어 보세요.</p>
        )}
      </nav>

      <Link href="/settings" className="sidebar-link" aria-current={path === "/settings" ? "page" : undefined}>
        <SettingsIcon />
        설정
      </Link>
      <details className="sidebar-manage">
        <summary>목록 정리</summary>
        <HistoryActions hasVisible={list.length > 0} />
      </details>
      <footer className="sidebar-foot">
        <span className="connection" data-state={state}>
          {connectionText[state]}
        </span>
        {status.data && <span className="sidebar-version">버전 {status.data.version}</span>}
      </footer>
    </aside>
  );
}

function SessionRow({ s, current }: { s: Session; current: boolean }) {
  const running = isRunningLive(s);
  return (
    <li className="session-row" data-state={s.state}>
      <Link href={sessionHref(s)} className="session-link" aria-current={current ? "page" : undefined}>
        <span className="session-title">
          {running && <span className="session-dot" data-state={s.state} aria-hidden="true" />}
          {s.title}
        </span>
        <span className="session-meta">
          {/* A running capture's counts grow every second; its own page shows them live. */}
          {running ? sessionKindText(s) : sessionSummary(s)}
          {s.state === "paused" && s.errorMessage ? ` (${s.errorMessage})` : ""}
        </span>
      </Link>
    </li>
  );
}

// Hides every saved session from the list, or shows them again. Nothing is deleted.
function HistoryActions({ hasVisible }: { hasVisible: boolean }) {
  const queryClient = useQueryClient();
  const change = useMutation({
    mutationFn: (restore: boolean) => (restore ? api.restoreSessions() : api.hideSessions()).then((r) => ({ ...r, restore })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["sessions"] }),
  });
  return (
    <div className="history">
      <p className="field-help">숨겨도 채팅과 내보낸 파일은 데이터 폴더에 그대로 남습니다.</p>
      <p className="actions">
        {hasVisible && (
          <button type="button" className="link-button" onClick={() => change.mutate(false)} disabled={change.isPending}>
            목록에서 모두 숨기기
          </button>
        )}
        <button type="button" className="link-button" onClick={() => change.mutate(true)} disabled={change.isPending}>
          숨긴 작업 다시 표시
        </button>
      </p>
      {change.data && (
        <p className="field-help" role="status">
          {change.data.restore
            ? change.data.changed > 0
              ? `${change.data.changed}개 작업을 다시 표시합니다.`
              : "숨긴 작업이 없습니다."
            : `${change.data.changed}개 작업을 목록에서 숨겼습니다.`}
        </p>
      )}
      {change.isError && (
        <p className="error" role="alert">
          {change.error.message}
        </p>
      )}
    </div>
  );
}

import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Session } from "../../api/types";
import { timecode } from "../../shared/format";
import { useJob } from "../../shared/useJob";

interface Props {
  s: Session;
  busy: boolean;
  /** The playhead; the lines around it are listed. */
  nowMs: number;
  durationMs: number;
  /** Jump the video (or the playhead) to a time. */
  onPick: (ms: number) => void;
}

const WINDOW_MS = 10 * 60 * 1000;

/**
 * What the broadcaster said (docs/AI-CUT-ANALYSIS.md 6.1): the newest
 * transcript's lines in ten-minute pages around the playhead, each a
 * button to that moment; a search over the page; and the way to run the
 * recognition (again).
 */
export function TranscriptPanel({ s, busy, nowMs, durationMs, onPick }: Props) {
  const transcript = useQuery({ queryKey: ["transcript", s.id, s.updatedAt], queryFn: () => api.transcript(s.id) });
  const [page, setPage] = useState<number | null>(null); // null: follow the playhead
  const current = page ?? Math.floor(nowMs / WINDOW_MS);
  const from = current * WINDOW_MS;
  const to = Math.min(durationMs || from + WINDOW_MS, from + WINDOW_MS);
  const lines = useQuery({
    queryKey: ["utterances", s.id, transcript.data?.id, from],
    queryFn: () => api.utterances(s.id, from, from + WINDOW_MS),
    enabled: !!transcript.data,
  });
  const [query, setQuery] = useState("");
  const run = useMutation({ mutationFn: () => api.transcribe(s.id) });
  const longForm = durationMs >= 3_600_000;
  const pages = Math.max(1, Math.ceil(Math.max(durationMs, 1) / WINDOW_MS));
  const shown = (lines.data?.utterances ?? []).filter((u) => !query.trim() || u.text.includes(query.trim()));
  const t = transcript.data;
  const job = useJob().data;
  const recognizing = !!job?.busy && job.sessionId === s.id && job.phase === "transcribing";

  return (
    <section className="panel-section" aria-labelledby="transcript-title">
      <div className="section-head">
        <h2 id="transcript-title" className="section-title">
          대본
        </h2>
        {t && (
          <span className="field-help">
            발언 {t.utterances.toLocaleString("ko-KR")}건 · {t.source === "asr" ? "이 컴퓨터에서 인식" : t.source === "file" ? "자막 파일" : "예시"}
          </span>
        )}
      </div>
      {transcript.isPending ? (
        <p className="empty">확인하는 중</p>
      ) : recognizing && !t ? (
        <p className="empty" aria-live="polite">
          방송 내용을 인식하는 중입니다 (이 컴퓨터에서만). {timecode(job.positionMs)} / {timecode(job.durationMs)}까지 인식, 발언{" "}
          {(job.utterances ?? 0).toLocaleString("ko-KR")}건. 끝나면 여기에 나타납니다.
        </p>
      ) : !t ? (
        <>
          <p className="empty">
            아직 방송 내용을 인식하지 않았습니다. 음성 파일이 연결된 작업(NAS 백업)이면 이 컴퓨터에서 인식해 발언을 채팅 옆에 두고, AI 컷 분석의
            근거로 씁니다.
          </p>
          <p className="actions">
            <button type="button" className="button" onClick={() => run.mutate()} disabled={busy || run.isPending}>
              방송 내용 인식
            </button>
          </p>
        </>
      ) : (
        <>
          <div className="transcript-tools">
            <input
              type="search"
              aria-label="대본 찾기"
              placeholder="이 10분에서 찾기"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            <span className="transcript-pager">
              <button type="button" className="transport-button" onClick={() => setPage(Math.max(0, current - 1))} disabled={current === 0}>
                이전 10분
              </button>
              <span className="timecode">
                {timecode(from, longForm)}–{timecode(to, longForm)}
              </span>
              <button type="button" className="transport-button" onClick={() => setPage(Math.min(pages - 1, current + 1))} disabled={current >= pages - 1}>
                다음 10분
              </button>
              {page !== null && (
                <button type="button" className="link-button" onClick={() => setPage(null)}>
                  재생 위치로
                </button>
              )}
            </span>
          </div>
          {lines.isPending ? (
            <p className="empty">불러오는 중</p>
          ) : shown.length === 0 ? (
            <p className="empty">{query.trim() ? "이 10분에는 그 말이 없습니다." : "이 10분에는 인식된 발언이 없습니다."}</p>
          ) : (
            <ol className="chatlog transcript-lines" aria-label="발언 목록" data-pickable>
              {shown.map((u) => (
                <li key={u.id} data-current={u.startMs <= nowMs && nowMs < u.endMs + 2000 || undefined}>
                  <button type="button" className="chat-row" title="이 발언으로 이동" onClick={() => onPick(u.startMs)}>
                    <span className="chat-time timecode">{timecode(u.startMs, longForm)}</span>
                    <span className="chat-author" data-tone={3}>
                      {u.speaker}
                    </span>
                    <span className="chat-text">{u.text}</span>
                  </button>
                </li>
              ))}
            </ol>
          )}
          <p className="actions">
            <button type="button" className="button button-quiet" onClick={() => run.mutate()} disabled={busy || run.isPending}>
              다시 인식
            </button>
            <span className="field-help">대본 파일(SRT·VTT)은 파일 탭에 있습니다.</span>
          </p>
        </>
      )}
      {run.isError && (
        <p className="error" role="alert">
          {run.error.message}
        </p>
      )}
    </section>
  );
}

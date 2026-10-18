import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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

const sourceText = { asr: "이 컴퓨터에서 인식", file: "자막 파일", demo: "예시" } as const;

/**
 * What the broadcaster said (docs/AI-CUT-ANALYSIS.md 6.1): the newest
 * transcript's lines in ten-minute pages around the playhead, each a
 * button to that moment; a search over the page; the way to run the
 * recognition (again); and a caption file (SRT·VTT) as another source.
 */
export function TranscriptPanel({ s, busy, nowMs, durationMs, onPick }: Props) {
  const queryClient = useQueryClient();
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
  const working = !!job?.busy && job.sessionId === s.id && (job.phase === "transcribing" || job.phase === "downloading");
  const canRecognize = !!s.transcribeSource;
  const afterImport = () => {
    void queryClient.invalidateQueries({ queryKey: ["transcript", s.id] });
    void queryClient.invalidateQueries({ queryKey: ["utterances", s.id] });
    void queryClient.invalidateQueries({ queryKey: ["timeline", s.id] });
    void queryClient.invalidateQueries({ queryKey: ["files", s.id] });
  };

  const recognizeButton = (quiet: boolean) => (
    <button
      type="button"
      className={quiet ? "button button-quiet" : "button"}
      onClick={() => run.mutate()}
      disabled={busy || run.isPending || !canRecognize}
      title={
        s.transcribeSource === "stream"
          ? "공개 재생 주소에서 가장 낮은 화질의 음성만 받아 이 컴퓨터에서 인식하고, 끝나면 받은 파일을 지웁니다."
          : s.transcribeSource === "media"
            ? "연결된 음성·영상 파일을 이 컴퓨터에서 인식합니다."
            : "이 작업에는 인식할 음성이 없습니다. 자막 파일을 넣을 수 있습니다."
      }
    >
      {t ? "다시 인식" : "방송 내용 인식"}
    </button>
  );

  return (
    <section className="panel-section" aria-labelledby="transcript-title">
      <div className="section-head">
        <h2 id="transcript-title" className="section-title">
          대본
        </h2>
        {t && (
          <span className="field-help">
            발언 {t.utterances.toLocaleString("ko-KR")}건 · {sourceText[t.source] ?? t.source}
          </span>
        )}
      </div>
      {transcript.isPending ? (
        <p className="empty">확인하는 중</p>
      ) : working && !t ? (
        <p className="empty" aria-live="polite">
          {job.phase === "downloading"
            ? `방송 음성을 받는 중입니다. ${timecode(job.positionMs)} / ${timecode(job.durationMs)}까지 받음. 받은 파일은 인식이 끝나면 지웁니다.`
            : `방송 내용을 인식하는 중입니다 (이 컴퓨터에서만). ${timecode(job.positionMs)} / ${timecode(job.durationMs)}까지 인식, 발언 ${(
                job.utterances ?? 0
              ).toLocaleString("ko-KR")}건.`}{" "}
          끝나면 여기에 나타납니다.
        </p>
      ) : !t ? (
        <>
          <p className="empty">
            {s.transcribeSource === "stream"
              ? "아직 방송 내용을 인식하지 않았습니다. 다시보기의 음성을 받아 이 컴퓨터에서 인식하면 발언이 채팅 옆에 놓이고 AI 컷 분석의 근거가 됩니다. 받은 음성은 끝나면 지웁니다."
              : s.transcribeSource === "media"
                ? "아직 방송 내용을 인식하지 않았습니다. 연결된 음성 파일을 이 컴퓨터에서 인식해 발언을 채팅 옆에 두고, AI 컷 분석의 근거로 씁니다."
                : "이 작업에는 인식할 음성이 없습니다. 자막 파일(SRT·VTT)이 있으면 아래에서 넣어 발언으로 씁니다."}
          </p>
          <p className="actions">{recognizeButton(false)}</p>
          <CaptionForm sessionId={s.id} busy={busy} onDone={afterImport} />
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
            {recognizeButton(true)}
            <span className="field-help">대본 파일(SRT·VTT)은 파일 탭에 있습니다.</span>
          </p>
          {working && (
            <p className="field-help" aria-live="polite">
              {job.phase === "downloading" ? "방송 음성을 받는 중입니다." : "다시 인식하는 중입니다."} 끝나면 새 대본으로 바뀝니다.
            </p>
          )}
          <CaptionForm sessionId={s.id} busy={busy} onDone={afterImport} replacing />
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

/**
 * A caption file as the transcript: an SRT or WebVTT file and the seconds
 * to shift it by (negative when the captions start later than the video).
 * Speaker names in the file become roles; the file itself is not kept.
 */
function CaptionForm({ sessionId, busy, onDone, replacing = false }: { sessionId: string; busy: boolean; onDone: () => void; replacing?: boolean }) {
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [offset, setOffset] = useState("0");
  const upload = useMutation({
    mutationFn: () => api.importCaption(sessionId, file!, Number(offset) * 1000),
    onSuccess: () => {
      setFile(null);
      if (input.current) input.current.value = "";
      onDone();
    },
  });
  const offsetOk = offset.trim() !== "" && Number.isFinite(Number(offset)) && Math.abs(Number(offset)) <= 24 * 3600;
  const id = `caption-${sessionId}`;

  return (
    <details className="field-more caption-form" open={!replacing || undefined}>
      <summary>{replacing ? "자막 파일로 바꾸기" : "자막 파일 넣기"}</summary>
      <form
        className="caption-fields"
        aria-label="자막 파일 넣기"
        onSubmit={(e) => {
          e.preventDefault();
          if (file && offsetOk) upload.mutate();
        }}
      >
        <label className="field">
          <span className="field-label">자막 파일 (SRT·VTT)</span>
          <input
            ref={input}
            id={id}
            type="file"
            accept=".srt,.vtt,text/vtt,application/x-subrip"
            onChange={(e) => {
              upload.reset();
              setFile(e.target.files?.[0] ?? null);
            }}
          />
        </label>
        <label className="field">
          <span className="field-label">시간 오프셋 (초)</span>
          <input
            type="number"
            inputMode="decimal"
            step="0.1"
            value={offset}
            onChange={(e) => {
              upload.reset();
              setOffset(e.target.value);
            }}
            aria-invalid={!offsetOk || undefined}
          />
        </label>
        <p className="field-help">
          자막의 모든 시각에 더합니다. 자막이 영상보다 3초 늦게 시작하면 -3을 적습니다. 파일의 화자 이름은 ‘방송자’·‘게스트 1’ 같은 역할명으로
          바뀌고, 파일 자체는 저장하지 않습니다.
        </p>
        <p className="actions">
          <button type="submit" className="button button-quiet" disabled={busy || upload.isPending || !file || !offsetOk}>
            {upload.isPending ? "넣는 중" : "자막 넣기"}
          </button>
          {upload.isSuccess && (
            <span className="field-help" role="status">
              자막 {upload.data.utterances.toLocaleString("ko-KR")}줄을 대본으로 넣었습니다.
            </span>
          )}
        </p>
        {upload.isError && (
          <p className="error" role="alert">
            {upload.error.message}
          </p>
        )}
      </form>
    </details>
  );
}

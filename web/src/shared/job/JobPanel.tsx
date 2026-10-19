import type { CSSProperties } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { JobStatus } from "../../api/types";
import { formatSize, timecode } from "../format";
import { Link } from "../navigation";

const phaseText: Record<string, string> = {
  starting: "시작하는 중",
  checking: "영상 정보 확인",
  collecting: "채팅 수집",
  analyzing: "편집 후보 찾기",
  comments: "영상 댓글 읽기",
  downloading: "방송 음성 받기",
  transcribing: "방송 내용 인식",
  bundling: "백업 묶음 만들기",
  video: "백업 묶음 영상 받기",
  done: "완료",
  paused: "중단됨",
  error: "중단됨",
};

/** Progress of the current job, with cancel while running and a link when done. */
export function JobPanel({ job, showResultLink = true }: { job: JobStatus; showResultLink?: boolean }) {
  const cancel = useMutation({ mutationFn: api.cancelJob });
  if (!job.id) return null;
  const collecting =
    (job.phase === "collecting" || job.phase === "downloading" || job.phase === "transcribing" || job.phase === "video") &&
    job.durationMs > 0;
  const failed = job.phase === "error" || job.phase === "paused";
  const longForm = job.durationMs >= 3_600_000;

  return (
    <div className="job" role="status" aria-live="polite" data-phase={job.phase}>
      <p className="job-head">
        <span className="job-phase">{phaseText[job.phase] ?? job.phase}</span>
        {job.title && <span className="job-title">{job.title}</span>}
      </p>
      {job.busy && (
        <div
          className="meter"
          role="progressbar"
          aria-label={phaseText[job.phase] ?? "진행"}
          data-state={collecting ? "determinate" : "indeterminate"}
          {...(collecting ? { "aria-valuemin": 0, "aria-valuemax": job.durationMs, "aria-valuenow": Math.min(job.positionMs, job.durationMs) } : {})}
        >
          <span className="meter-fill" style={{ "--p": collecting ? Math.min(1, job.positionMs / job.durationMs) : 0 } as CSSProperties} />
        </div>
      )}
      {collecting && (
        <>
          <p className="job-meta">
            <span className="timecode">
              {timecode(job.positionMs, longForm)} / {timecode(job.durationMs, longForm)}
            </span>
            {job.phase === "transcribing"
              ? `까지 인식, 발언 ${(job.utterances ?? 0).toLocaleString("ko-KR")}건`
              : job.phase === "downloading"
                ? `까지 받음, 조각 ${job.pages}개`
                : job.phase === "video"
                  ? `까지 받음, 조각 ${job.pages}개, ${formatSize(job.bytes ?? 0)}`
                : `까지 받음, 페이지 ${job.pages}개`}
          </p>
        </>
      )}
      <p className={failed ? "job-message error" : "job-message"}>{job.message}</p>
      {job.warning && <p className="job-message">{job.warning}</p>}
      <p className="actions">
        {job.busy && (
          <button type="button" className="button button-quiet" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
            작업 중단
          </button>
        )}
        {!job.busy && job.sessionId && showResultLink && (
          <Link href={`/sessions/${job.sessionId}`} className="button">
            {job.phase === "done" ? "결과 보기" : "저장된 작업 열기"}
          </Link>
        )}
      </p>
    </div>
  );
}

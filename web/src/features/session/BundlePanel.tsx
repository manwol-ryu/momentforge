import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { BundleView, Session } from "../../api/types";
import { formatSize } from "../../shared/format";
import { useJob } from "../../shared/useJob";

const videoText: Record<BundleView["videoState"], string> = {
  complete: "영상 포함 (video.mp4)",
  partial: "영상 받다가 멈춤 — 다시 누르면 이어받습니다",
  ref: "영상은 원래 위치를 적음",
  none: "영상 없음",
};

/**
 * The session backup bundle (docs/AI-CUT-ANALYSIS.md 2.5): one folder with
 * the video (or where it is), chat, transcript, timeline, analysis, cut
 * plans and notes, so the broadcast can be edited again without the app.
 */
export function BundlePanel({ s, busy }: { s: Session; busy: boolean }) {
  const job = useJob().data;
  const jobHere = job?.kind === "bundle" && job.sessionId === s.id ? job : undefined;
  const bundle = useQuery({
    // A finished or stopped bundle job changes what is in the folder.
    queryKey: ["bundle", s.id, jobHere?.id, jobHere?.busy],
    queryFn: () => api.bundle(s.id),
  });
  const v = bundle.data;
  const [video, setVideo] = useState<boolean | undefined>(undefined);
  useEffect(() => {
    if (v && video === undefined) setVideo(v.videoOn);
  }, [v, video]);
  const start = useMutation({ mutationFn: () => api.startBundle(s.id, v?.videoSource === "download" && !!video) });
  const working = !!jobHere?.busy;

  if (bundle.isError) {
    return (
      <p className="error" role="alert">
        {bundle.error.message}
      </p>
    );
  }
  if (!v) return null;
  const label = !v.exists ? "백업 묶음 만들기" : v.videoState === "partial" && video ? "영상 이어받기" : "백업 묶음 갱신";

  return (
    <section className="panel-section bundle" aria-labelledby="bundle-title">
      <h2 id="bundle-title" className="section-title">
        백업 묶음
      </h2>
      <p className="field-help">
        이 폴더만으로 다시 편집하고 다시 분석할 수 있게 영상·채팅·대본·분석·컷 계획·메모를 한곳에 모읍니다. 다시 만들면 바뀐 파일만
        바꿉니다.
      </p>
      {v.exists ? (
        <dl className="facts facts-stacked bundle-facts">
          <dt>위치</dt>
          <dd className="bundle-path">{v.dir}</dd>
          <dt>내용</dt>
          <dd>
            파일 {v.files.toLocaleString("ko-KR")}개 · {formatSize(v.bytes)} · {videoText[v.videoState]}
          </dd>
          {v.updatedAt && (
            <>
              <dt>마지막으로 만든 시각</dt>
              <dd>{new Date(v.updatedAt).toLocaleString("ko-KR")}</dd>
            </>
          )}
        </dl>
      ) : (
        <p className="field-help">
          만들 위치: <span className="bundle-path">{v.dir}</span>
        </p>
      )}
      {v.videoSource === "download" && (
        <label className="check check-option">
          <input type="checkbox" checked={!!video} onChange={(e) => setVideo(e.target.checked)} disabled={working} />
          <span>
            영상 포함 ({v.videoHeight}p 이하)
            <span className="check-help">
              공개 다시보기를 재인코딩 없이 video.mp4로 받습니다. 시작 시각을 0초로 맞춰 채팅·컷 시각과 그대로 맞습니다.
            </span>
          </span>
        </label>
      )}
      {v.videoSource === "ref" && (
        <p className="field-help">
          NAS 백업 영상은 복사하지 않고 위치만 적습니다: <span className="bundle-path">{v.videoRef}</span>
        </p>
      )}
      {v.videoSource === "" && <p className="field-help">이 작업에는 받을 수 있는 영상이 없어 영상 없이 만듭니다.</p>}
      <p className="actions">
        <button
          type="button"
          className="button"
          onClick={() => start.mutate()}
          disabled={busy || start.isPending || s.state === "collecting"}
        >
          {label}
        </button>
        {working && <span className="field-help">작업 카드에서 진행을 보고 중단할 수 있습니다.</span>}
      </p>
      {start.isError && (
        <p className="error" role="alert">
          {start.error.message}
        </p>
      )}
    </section>
  );
}

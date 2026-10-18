import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Settings } from "../../api/types";
import type { UrlFieldProps } from "./Home";

/** Collects a VOD's chat (and its comments) and finds the moments to edit. */
interface Props extends UrlFieldProps {
  busy: boolean;
  defaults: Settings;
  /** A job was started: the screen follows it to its saved work. */
  onStarted: (jobId: string) => void;
}

export function CollectForm({ url, onUrl, onPaste, focus, moved, hint, busy, defaults, onStarted }: Props) {
  // The options start from the settings screen's defaults; a change here is for this job only.
  const [sensitive, setSensitive] = useState(defaults.sensitive);
  const [withComments, setWithComments] = useState(defaults.includeComments);
  const input = useRef<HTMLInputElement>(null);
  const collect = useMutation({
    mutationFn: () => api.collect(url.trim(), sensitive, withComments),
    onSuccess: (job) => onStarted(job.id),
  });
  const commentsOnly = useMutation({
    mutationFn: () => api.startComments({ url: url.trim() }),
    onSuccess: (job) => onStarted(job.id),
  });
  useEffect(() => {
    if (focus) input.current?.focus();
  }, [focus]);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    collect.mutate();
  };
  const failed = collect.error ?? commentsOnly.error;
  return (
    <form className="collect" onSubmit={submit} aria-label="다시보기 수집">
      {moved && (
        <p className="moved" role="status">
          다시보기 주소라서 다시보기 수집으로 옮겼습니다.
        </p>
      )}
      <label className="field">
        <span className="field-label">치지직 다시보기 주소</span>
        <input
          ref={input}
          type="url"
          inputMode="url"
          required
          placeholder="https://chzzk.naver.com/video/영상번호"
          value={url}
          onChange={(e) => onUrl(e.target.value)}
          onPaste={(e) => onPaste(e.clipboardData.getData("text"))}
          aria-describedby="collect-help"
        />
      </label>
      {hint}
      <label className="check check-option">
        <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
        <span>
          항상 가장 민감하게
          <span className="check-help">10초에 채팅 6건부터 판정합니다. 작은 반응도 찾지만 잘못 잡는 후보가 늘 수 있습니다.</span>
        </span>
      </label>
      <label className="check check-option">
        <input type="checkbox" checked={withComments} onChange={(e) => setWithComments(e.target.checked)} />
        <span>
          영상 댓글의 시청자 추천도 수집
          <span className="check-help">댓글에 적힌 시각을 자동 후보와 따로 표시합니다.</span>
        </span>
      </label>
      <p id="collect-help" className="field-help">
        채팅을 모두 받은 뒤 편집 후보를 찾습니다. 시작하면 작업 화면으로 옮겨 가 진행 상황을 보여 줍니다. 긴 방송은 몇 분 걸릴 수
        있고, 중단해도 받은 데까지 저장됩니다.
      </p>
      <p className="actions">
        <button type="submit" className="button" disabled={busy || collect.isPending}>
          수집하고 편집점 찾기
        </button>
        <button
          type="button"
          className="button button-quiet"
          onClick={() => url.trim() && commentsOnly.mutate()}
          disabled={busy || commentsOnly.isPending || !url.trim()}
        >
          댓글만 수집
        </button>
      </p>
      {failed && (
        <p className="error" role="alert">
          {failed.message}
        </p>
      )}
    </form>
  );
}

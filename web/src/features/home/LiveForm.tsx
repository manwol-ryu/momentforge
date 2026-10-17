import { useEffect, useRef, useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Session } from "../../api/types";
import { Link, navigate } from "../../shared/navigation";
import type { UrlFieldProps } from "./Home";

/** Checks a channel and starts a LIVE capture; the capture opens in its own view. */
export function LiveForm({ url, onUrl, onPaste, focus, moved, hint, running }: UrlFieldProps & { running?: Session }) {
  const queryClient = useQueryClient();
  const [sensitive, setSensitive] = useState(false);
  const [viewerAdaptive, setViewerAdaptive] = useState(true);
  const input = useRef<HTMLInputElement>(null);
  const inspect = useMutation({ mutationFn: () => api.inspectLive(url.trim()) });
  const start = useMutation({
    mutationFn: () => api.startLive(url.trim(), sensitive, viewerAdaptive),
    onSuccess: (s) => {
      void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      navigate(`/live/${s.id}`);
    },
  });
  useEffect(() => {
    if (focus) input.current?.focus();
  }, [focus]);
  // A changed address makes the last check stale.
  const resetInspect = inspect.reset;
  useEffect(() => resetInspect(), [url, resetInspect]);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    start.mutate();
  };
  const info = inspect.data;
  const failed = start.error ?? inspect.error;

  return (
    <form className="collect" onSubmit={submit} aria-label="생방송 수집">
      {moved && (
        <p className="moved" role="status">
          생방송 주소라서 생방송 수집으로 옮겼습니다.
        </p>
      )}
      {running && (
        <p className="notice">
          지금 <Link href={`/live/${running.id}`}>{running.title}</Link> 생방송을 수집하고 있습니다. 다른 방송은 이 수집을 중단한 뒤
          시작할 수 있습니다.
        </p>
      )}
      <label className="field">
        <span className="field-label">치지직 생방송 주소</span>
        <input
          ref={input}
          type="url"
          inputMode="url"
          required
          placeholder="https://chzzk.naver.com/live/채널ID"
          value={url}
          onChange={(e) => onUrl(e.target.value)}
          onPaste={(e) => onPaste(e.clipboardData.getData("text"))}
          aria-describedby="live-help"
        />
      </label>
      {hint}
      <label className="check">
        <input type="checkbox" checked={sensitive} onChange={(e) => setSensitive(e.target.checked)} />
        항상 가장 민감하게 (10초에 채팅 6건부터 판정)
      </label>
      <label className="check">
        <input type="checkbox" checked={viewerAdaptive} onChange={(e) => setViewerAdaptive(e.target.checked)} />
        시청자 수 변화 보정 (시청자가 기준보다 10% 넘게 줄면 평소 채팅량 기준을 다시 학습합니다)
      </label>
      <p id="live-help" className="field-help">
        공개 채팅을 받는 동안 편집 후보를 바로 찾습니다. 처음 3분 동안 채널을 지켜본 뒤, 판정 기준을 최근 2시간과 최근 15분의
        평소 채팅량에 맞춰 자동으로 정합니다. 수집을 시작한 시점 이전 채팅은 받을 수 없고, 연결이 끊긴 구간은 따로 표시합니다.
      </p>
      {info && (
        <p className="live-inspect" role="status">
          <strong>{info.title || "제목 없음"}</strong>{" "}
          {info.open
            ? `방송 중${info.viewers !== null ? `, 시청자 ${info.viewers.toLocaleString("ko-KR")}명` : ""}. 수집 시작을 누르면 채팅에 연결합니다.`
            : "지금은 생방송 중이 아닙니다."}
        </p>
      )}
      <p className="actions">
        <button type="submit" className="button" disabled={start.isPending}>
          수집 시작
        </button>
        <button
          type="button"
          className="button button-quiet"
          onClick={() => url.trim() && inspect.mutate()}
          disabled={inspect.isPending || !url.trim()}
        >
          방송 확인
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

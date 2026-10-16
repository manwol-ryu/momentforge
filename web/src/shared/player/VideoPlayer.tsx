import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { api, ApiError } from "../../api/client";

export interface PlayerHandle {
  /** Moves the playhead to ms and starts playing. */
  seek(ms: number): void;
}

interface Props {
  sessionId: string;
  onTime: (ms: number) => void;
}

type State = { kind: "loading" } | { kind: "ready" } | { kind: "error"; message: string };

/**
 * Plays the platform's public HLS stream. The signed URL is fetched when the
 * player mounts and never stored. hls.js loads only when the browser cannot
 * play HLS natively (Safari can).
 */
export const VideoPlayer = forwardRef<PlayerHandle, Props>(function VideoPlayer({ sessionId, onTime }, ref) {
  const video = useRef<HTMLVideoElement>(null);
  const [state, setState] = useState<State>({ kind: "loading" });

  useImperativeHandle(ref, () => ({
    seek(ms: number) {
      const v = video.current;
      if (!v) return;
      v.currentTime = ms / 1000;
      void v.play().catch(() => undefined); // autoplay may be blocked; the position still moves
    },
  }));

  useEffect(() => {
    let destroyed = false;
    let cleanup = () => {};
    (async () => {
      try {
        const { url } = await api.playback(sessionId);
        const v = video.current;
        if (destroyed || !v) return;
        if (v.canPlayType("application/vnd.apple.mpegurl")) {
          v.src = url;
        } else {
          // The light build drops DRM, subtitles and alternate audio, which public VODs do not use.
          const { default: Hls } = await import("hls.js/light");
          if (destroyed) return;
          if (!Hls.isSupported()) throw new Error("이 브라우저는 앱 안 영상 재생을 지원하지 않습니다.");
          const hls = new Hls();
          hls.on(Hls.Events.ERROR, (_e, data) => {
            if (data.fatal) setState({ kind: "error", message: "영상을 불러오지 못했습니다. 치지직에서 보기 링크를 사용하세요." });
          });
          hls.loadSource(url);
          hls.attachMedia(v);
          cleanup = () => hls.destroy();
        }
        setState({ kind: "ready" });
      } catch (e) {
        if (!destroyed) setState({ kind: "error", message: e instanceof ApiError || e instanceof Error ? e.message : String(e) });
      }
    })();
    return () => {
      destroyed = true;
      cleanup();
    };
  }, [sessionId]);

  return (
    <div className="player">
      <video
        ref={video}
        className="player-video"
        controls
        playsInline
        preload="metadata"
        aria-label="다시보기 영상"
        onTimeUpdate={(e) => onTime(e.currentTarget.currentTime * 1000)}
        onSeeked={(e) => onTime(e.currentTarget.currentTime * 1000)}
      />
      {state.kind === "loading" && <p className="player-note">영상을 불러오는 중입니다.</p>}
      {state.kind === "error" && (
        <p className="player-note error" role="alert">
          {state.message}
        </p>
      )}
    </div>
  );
});

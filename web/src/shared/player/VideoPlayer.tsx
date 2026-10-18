import { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { HlsConfig } from "hls.js";
import { api, ApiError } from "../../api/client";

export interface PlayerHandle {
  /** Moves the playhead to ms and, unless play is false, starts playing. */
  seek(ms: number, play?: boolean): void;
}

interface Props {
  sessionId: string;
  /** The channel's current broadcast (a LIVE capture) instead of a VOD. */
  live?: boolean;
  /** Where else to watch, added when the stream stops loading (live only). */
  fallback?: string;
  onTime?: (ms: number) => void;
  /**
   * A VOD's transport under the picture (skip back and forward, play and
   * pause) and the matching keys for the whole page: ← → 5 s, Shift 30 s,
   * Space play/pause. The browser's own keys would otherwise skip by a
   * share of the length (minutes on a long broadcast) or do nothing,
   * depending on what had the focus.
   */
  transport?: boolean;
}

type State = { kind: "loading" } | { kind: "ready" } | { kind: "playing" } | { kind: "error"; message: string };

export const SKIP_MS = 5000;
export const SKIP_LONG_MS = 30000;

/**
 * Timed metadata (ID3, the EXT-X-DATERANGE tags of CHZZK live playlists) is unused,
 * and hls.js would show it through a `data:` track the page's CSP blocks.
 */
const baseConfig: Partial<HlsConfig> = {
  enableID3MetadataCues: false,
  enableEmsgMetadataCues: false,
  enableDateRangeMetadataCues: false,
};

/**
 * A live stream downloads only while it plays. CHZZK's live playlists
 * declare 10 s segments but carry 2 s ones over a 30 s window, so the default
 * delay of three target durations would start at the oldest segment; a delay
 * in seconds keeps playback near the edge instead (hls.js stays at least one
 * target duration back: about 11 s in the 2026-10 check). The back buffer is
 * capped because a capture can stay open for hours.
 */
const liveConfig: Partial<HlsConfig> = {
  ...baseConfig,
  autoStartLoad: false,
  lowLatencyMode: false,
  liveDurationInfinity: true,
  liveSyncDuration: 6,
  liveMaxLatencyDuration: 20,
  backBufferLength: 30,
};

/** Keys typed into a field, a tab list or a choice belong to that control, not the player. */
function keyBelongsToControl(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable || target.closest('[role="tab"], [role="tablist"], [role="menu"], [role="listbox"]')) return true;
  if (target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return true;
  if (target instanceof HTMLInputElement) return !["range", "checkbox", "button", "submit"].includes(target.type);
  return false;
}

/**
 * Plays the platform's public HLS stream. The signed URL is fetched when the
 * player mounts and never stored. hls.js plays it wherever Media Source
 * Extensions exist: Chromium's own HLS support (Chrome 141 and later) seeks
 * by downloading from the start, so a candidate hours in never plays. The
 * browser's own player is only the fallback (iPhone Safari).
 */
export const VideoPlayer = forwardRef<PlayerHandle, Props>(function VideoPlayer(
  { sessionId, live = false, fallback, onTime, transport = false },
  ref,
) {
  const video = useRef<HTMLVideoElement>(null);
  const [state, setState] = useState<State>({ kind: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [paused, setPaused] = useState(true);

  const skip = (deltaMs: number) => {
    const v = video.current;
    if (!v) return;
    const end = Number.isFinite(v.duration) ? v.duration : Infinity;
    v.currentTime = Math.min(end, Math.max(0, v.currentTime + deltaMs / 1000));
  };
  const toggle = () => {
    const v = video.current;
    if (!v) return;
    if (v.paused) void v.play().catch(() => undefined);
    else v.pause();
  };

  useImperativeHandle(ref, () => ({
    seek(ms: number, play = true) {
      const v = video.current;
      if (!v) return;
      v.currentTime = ms / 1000;
      if (!play) return; // scrubbing the timeline only moves the position
      void v.play().catch(() => undefined); // autoplay may be blocked; the position still moves
      // A candidate far down the list would otherwise play out of sight.
      const r = v.getBoundingClientRect();
      if (r.top < 0 || r.bottom > window.innerHeight) {
        const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        v.scrollIntoView({ block: "center", behavior: reduce ? "auto" : "smooth" });
      }
    },
  }));

  // The page's transport keys, taken before the browser's own player handles them.
  useEffect(() => {
    if (!transport) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey || keyBelongsToControl(e.target)) return;
      if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
        e.preventDefault();
        skip((e.key === "ArrowRight" ? 1 : -1) * (e.shiftKey ? SKIP_LONG_MS : SKIP_MS));
      } else if (e.key === " " || e.code === "KeyK") {
        // Space on a button or link presses it; elsewhere it plays and pauses.
        const t = e.target;
        if (e.key === " " && t instanceof HTMLElement && t !== video.current && t.closest("button, a, input, summary")) return;
        e.preventDefault();
        toggle();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [transport]);

  useEffect(() => {
    let destroyed = false;
    let cleanup = () => {};
    setState({ kind: "loading" });
    (async () => {
      try {
        const { url, type } = await api.playback(sessionId);
        const v = video.current;
        if (destroyed || !v) return;
        if (type === "file") {
          // A media file streamed by this server (an imported backup): the browser's player seeks with range requests.
          v.src = url;
          setState({ kind: "ready" });
          return;
        }
        // The light build drops DRM, subtitles and alternate audio, which public streams do not use.
        const { default: Hls } = await import("hls.js/light");
        if (destroyed) return;
        if (Hls.isSupported()) {
          const hls = new Hls(live ? liveConfig : baseConfig);
          hls.on(Hls.Events.ERROR, (_e, data) => {
            if (!data.fatal) return;
            setState({
              kind: "error",
              message: live
                ? `방송 영상을 불러오지 못했습니다. 방송이 끝났거나 연결이 끊겼을 수 있습니다.${fallback ? ` ${fallback}` : ""}`
                : "영상을 불러오지 못했습니다. 치지직에서 보기 링크를 사용하세요.",
            });
          });
          hls.loadSource(url);
          hls.attachMedia(v);
          if (live) {
            // Pausing stops the download; playing again picks up near the live edge.
            const start = () => hls.startLoad(-1);
            const stop = () => hls.stopLoad();
            v.addEventListener("play", start);
            v.addEventListener("pause", stop);
            cleanup = () => {
              v.removeEventListener("play", start);
              v.removeEventListener("pause", stop);
              hls.destroy();
            };
          } else {
            cleanup = () => hls.destroy();
          }
        } else if (v.canPlayType("application/vnd.apple.mpegurl")) {
          v.src = url;
        } else {
          throw new Error("이 브라우저는 앱 안 영상 재생을 지원하지 않습니다.");
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
  }, [sessionId, live, fallback, attempt]);

  // A live stream that failed has nothing to play: the monitor goes dark with the reason.
  const dark = live && state.kind === "error";
  const error = state.kind === "error" && (
    <p className="player-note error" role="alert">
      {state.message}
      {live && (
        <>
          {" "}
          <button type="button" className="link-button" onClick={() => setAttempt((n) => n + 1)}>
            다시 불러오기
          </button>
        </>
      )}
    </p>
  );
  const canPlay = state.kind === "ready" || state.kind === "playing";
  return (
    <div className="player">
      <video
        ref={video}
        className="player-video"
        hidden={dark}
        controls
        playsInline
        preload={live ? "none" : "metadata"}
        aria-label={live ? "생방송 영상" : "다시보기 영상"}
        onPlaying={() => setState((s) => (s.kind === "ready" ? { kind: "playing" } : s))}
        onPlay={() => setPaused(false)}
        onPause={() => setPaused(true)}
        onTimeUpdate={onTime && ((e) => onTime(e.currentTarget.currentTime * 1000))}
        onSeeked={onTime && ((e) => onTime(e.currentTarget.currentTime * 1000))}
      />
      {state.kind === "loading" && <p className="player-note">영상을 불러오는 중입니다.</p>}
      {state.kind === "ready" && live && (
        <p className="player-note">재생하면 영상을 받기 시작합니다. 영상은 채팅보다 10초 넘게 늦을 수 있습니다.</p>
      )}
      {dark ? <div className="player-off">{error}</div> : error}
      {transport && (
        <div className="transport" role="group" aria-label="재생 조작">
          <button type="button" className="transport-button" onClick={() => skip(-SKIP_LONG_MS)} disabled={!canPlay}>
            −{SKIP_LONG_MS / 1000}초
          </button>
          <button type="button" className="transport-button" onClick={() => skip(-SKIP_MS)} disabled={!canPlay}>
            −{SKIP_MS / 1000}초
          </button>
          <button type="button" className="transport-button transport-play" onClick={toggle} disabled={!canPlay} aria-pressed={!paused}>
            {paused ? "재생" : "일시정지"}
          </button>
          <button type="button" className="transport-button" onClick={() => skip(SKIP_MS)} disabled={!canPlay}>
            +{SKIP_MS / 1000}초
          </button>
          <button type="button" className="transport-button" onClick={() => skip(SKIP_LONG_MS)} disabled={!canPlay}>
            +{SKIP_LONG_MS / 1000}초
          </button>
          <span className="transport-keys">
            <kbd>←</kbd>
            <kbd>→</kbd> {SKIP_MS / 1000}초 · <kbd>Shift</kbd>+방향키 {SKIP_LONG_MS / 1000}초 · <kbd>Space</kbd> 재생 · <kbd>N</kbd>
            <kbd>P</kbd> 다음·이전 후보
          </span>
        </div>
      )}
    </div>
  );
});

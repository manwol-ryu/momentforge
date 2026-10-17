import type { CSSProperties } from "react";
import type { Candidate, DetectWindow, Gap } from "../../api/types";
import { tickStep, timecode } from "../format";

interface Props {
  durationMs: number;
  windows: DetectWindow[];
  candidates: Candidate[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  /** Step between evaluation times; each bar covers one step. */
  stepMs?: number;
  /** Length of the span each bar counts (VOD: 10 s windows every 5 s). */
  windowMs?: number;
  /** Spans with no observed chat, shown hatched. */
  gaps?: Gap[];
  /** Moments from other sources (viewer comments, AI), drawn as their own markers. */
  extraMarkers?: { id: string; ms: number; kind: "viewer_comment" | "ai"; label: string }[];
  /** The playhead: where the video (or, without one, the chat view) is. */
  playheadMs?: number;
  /** Moves the playhead; the track becomes a slider for pointer and keyboard. */
  onScrub?: (ms: number) => void;
  /** Accessible name of that slider. */
  scrubLabel?: string;
}

const BAR_AREA = 64; // px, matches .timeline-bars height

/**
 * Reaction density over the whole timeline: one bar per evaluation step
 * (messages in the 10 s window), candidate ranges as bands, and a marker
 * per candidate at its peak. Markers are buttons, so the timeline is usable
 * with a keyboard as well as a pointer. With a playhead, a line shows it and
 * a see-through slider over the track moves it, like an editor's timeline.
 */
export function Timeline({
  durationMs,
  windows,
  candidates,
  selectedId,
  onSelect,
  stepMs = 5000,
  windowMs = 10_000,
  gaps = [],
  extraMarkers = [],
  playheadMs,
  onScrub,
  scrubLabel = "재생 위치",
}: Props) {
  const maxN = Math.max(1, ...windows.map((w) => w.n));
  const pct = (ms: number) => `${(Math.min(Math.max(ms, 0), durationMs) / durationMs) * 100}%`;
  const ticks: number[] = [];
  for (let t = 0; t <= durationMs; t += tickStep(durationMs)) ticks.push(t);
  const longForm = durationMs >= 3_600_000;
  const peakWindow = windows.reduce<DetectWindow | null>((a, w) => (!a || w.n > a.n ? w : a), null);

  return (
    <figure className="timeline">
      <div className="timeline-track">
        <svg
          className="timeline-bars"
          viewBox={`0 0 ${durationMs} ${BAR_AREA}`}
          preserveAspectRatio="none"
          role="img"
          aria-label={`반응 밀도 그래프. ${timecode(durationMs, longForm)} 길이, 가장 많은 채팅은 ${
            peakWindow ? `${timecode(peakWindow.t - windowMs, longForm)}부터 ${windowMs / 1000}초 동안 ${peakWindow.n}건` : "없음"
          }.${gaps.length > 0 ? ` 채팅을 받지 못한 구간 ${gaps.length}개.` : ""}`}
        >
          {windows.map((w) => {
            const h = w.valid ? Math.max(1, (w.n / maxN) * BAR_AREA) : 3;
            return (
              <rect
                key={w.t}
                className={w.valid ? "bar" : "bar bar-invalid"}
                x={w.t - stepMs}
                y={BAR_AREA - h}
                width={stepMs}
                height={h}
              />
            );
          })}
        </svg>
        {gaps.map((g) => (
          <div
            key={g.startMs}
            className="timeline-gap"
            style={{ left: pct(g.startMs), width: pct(g.endMs - g.startMs) }}
          />
        ))}
        {candidates.map((c) => (
          <div
            key={c.id}
            className="timeline-band"
            data-selected={c.id === selectedId || undefined}
            style={{ left: pct(c.reactionStartMs), width: pct(c.reactionEndMs - c.reactionStartMs) }}
          />
        ))}
        {playheadMs !== undefined && <div className="timeline-playhead" style={{ left: pct(playheadMs) }} aria-hidden="true" />}
        {onScrub && (
          <input
            type="range"
            className="timeline-scrub"
            min={0}
            max={durationMs}
            step={5000}
            value={Math.min(Math.max(playheadMs ?? 0, 0), durationMs)}
            onChange={(e) => onScrub(Number(e.target.value))}
            aria-label={scrubLabel}
            aria-valuetext={timecode(playheadMs ?? 0, longForm)}
          />
        )}
      </div>
      <div className="timeline-markers">
        {candidates.map((c, i) => (
          <button
            key={c.id}
            type="button"
            className="timeline-marker"
            style={{ left: pct(c.peakMs), "--at": c.peakMs / durationMs } as CSSProperties}
            aria-pressed={c.id === selectedId}
            aria-label={`후보 ${i + 1}, ${timecode(c.reactionStartMs, longForm)}`}
            onClick={() => onSelect(c.id)}
          />
        ))}
        {extraMarkers.map((m) => (
          <button
            key={m.id}
            type="button"
            className="timeline-marker"
            data-kind={m.kind}
            style={{ left: pct(m.ms), "--at": m.ms / durationMs } as CSSProperties}
            aria-pressed={m.id === selectedId}
            aria-label={m.label}
            onClick={() => onSelect(m.id)}
          />
        ))}
      </div>
      <div className="timeline-axis" aria-hidden="true">
        {ticks.map((t) => (
          <span key={t} className="timecode" style={{ left: pct(t) }}>
            {timecode(t, longForm)}
          </span>
        ))}
      </div>
    </figure>
  );
}

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
  /** The part of the timeline drawn (zoomed in); the whole length when absent. */
  viewStartMs?: number;
  viewEndMs?: number;
}

const BAR_AREA = 64; // px, matches .timeline-bars height

/**
 * Reaction density over the timeline: one bar per evaluation step
 * (messages in the 10 s window), candidate ranges as bands, and a marker
 * per candidate at its peak. Markers are buttons, so the timeline is usable
 * with a keyboard as well as a pointer. With a playhead, a line shows it and
 * a see-through slider over the track moves it, like an editor's timeline.
 * Zoomed in (a view range), it draws only that part, and a thin overview
 * under it shows where the part lies in the whole length.
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
  viewStartMs = 0,
  viewEndMs = durationMs,
}: Props) {
  const from = Math.max(0, Math.min(viewStartMs, durationMs));
  const to = Math.max(from + 1, Math.min(viewEndMs, durationMs));
  const span = to - from;
  const zoomed = from > 0 || to < durationMs;
  const inView = (ms: number) => ms >= from && ms <= to;
  const overlaps = (a: number, b: number) => b >= from && a <= to;
  const shown = windows.filter((w) => overlaps(w.t - stepMs, w.t));
  const maxN = Math.max(1, ...shown.map((w) => w.n));
  const pct = (ms: number) => `${((Math.min(Math.max(ms, from), to) - from) / span) * 100}%`;
  const width = (a: number, b: number) => `${((Math.min(b, to) - Math.max(a, from)) / span) * 100}%`;
  const longForm = durationMs >= 3_600_000;
  const tick = tickStep(span);
  const ticks: number[] = [];
  for (let t = Math.ceil(from / tick) * tick; t <= to; t += tick) ticks.push(t);
  const peakWindow = windows.reduce<DetectWindow | null>((a, w) => (!a || w.n > a.n ? w : a), null);

  return (
    <figure className="timeline" data-zoomed={zoomed || undefined}>
      <div className="timeline-track">
        <svg
          className="timeline-bars"
          viewBox={`${from} 0 ${span} ${BAR_AREA}`}
          preserveAspectRatio="none"
          role="img"
          aria-label={`반응 밀도 그래프. ${timecode(durationMs, longForm)} 길이, 가장 많은 채팅은 ${
            peakWindow ? `${timecode(peakWindow.t - windowMs, longForm)}부터 ${windowMs / 1000}초 동안 ${peakWindow.n}건` : "없음"
          }.${gaps.length > 0 ? ` 채팅을 받지 못한 구간 ${gaps.length}개.` : ""}${
            zoomed ? ` 지금 ${timecode(from, longForm)}부터 ${timecode(to, longForm)}까지 확대해 보는 중.` : ""
          }`}
        >
          {shown.map((w) => {
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
        {gaps
          .filter((g) => overlaps(g.startMs, g.endMs))
          .map((g) => (
            <div key={g.startMs} className="timeline-gap" style={{ left: pct(g.startMs), width: width(g.startMs, g.endMs) }} />
          ))}
        {candidates
          .filter((c) => overlaps(c.reactionStartMs, c.reactionEndMs))
          .map((c) => (
            <div
              key={c.id}
              className="timeline-band"
              data-selected={c.id === selectedId || undefined}
              style={{ left: pct(c.reactionStartMs), width: width(c.reactionStartMs, c.reactionEndMs) }}
            />
          ))}
        {playheadMs !== undefined && inView(playheadMs) && (
          <div className="timeline-playhead" style={{ left: pct(playheadMs) }} aria-hidden="true" />
        )}
        {onScrub && (
          <input
            type="range"
            className="timeline-scrub"
            min={from}
            max={to}
            step={5000}
            value={Math.min(Math.max(playheadMs ?? from, from), to)}
            onChange={(e) => onScrub(Number(e.target.value))}
            aria-label={scrubLabel}
            aria-valuetext={timecode(playheadMs ?? 0, longForm)}
          />
        )}
      </div>
      <div className="timeline-markers">
        {candidates.map((c, i) =>
          inView(c.peakMs) ? (
            <button
              key={c.id}
              type="button"
              className="timeline-marker"
              style={{ left: pct(c.peakMs), "--at": (c.peakMs - from) / span } as CSSProperties}
              aria-pressed={c.id === selectedId}
              aria-label={`후보 ${i + 1}, ${timecode(c.reactionStartMs, longForm)}`}
              title={`후보 ${i + 1} · ${timecode(c.reactionStartMs, longForm)} · ${c.reasons.join(", ")}`}
              onClick={() => onSelect(c.id)}
            />
          ) : null,
        )}
        {extraMarkers
          .filter((m) => inView(m.ms))
          .map((m) => (
            <button
              key={m.id}
              type="button"
              className="timeline-marker"
              data-kind={m.kind}
              style={{ left: pct(m.ms), "--at": (m.ms - from) / span } as CSSProperties}
              aria-pressed={m.id === selectedId}
              aria-label={m.label}
              title={m.label}
              onClick={() => onSelect(m.id)}
            />
          ))}
      </div>
      <div className="timeline-axis" aria-hidden="true">
        {ticks.map((t) => {
          const at = (t - from) / span;
          return (
            <span key={t} className="timecode" data-edge={at < 0.04 ? "start" : at > 0.96 ? "end" : undefined} style={{ left: pct(t) }}>
              {timecode(t, longForm)}
            </span>
          );
        })}
      </div>
      {zoomed && (
        <div className="timeline-overview" aria-hidden="true">
          {candidates.map((c) => (
            <span key={c.id} className="overview-mark" style={{ left: `${(c.peakMs / durationMs) * 100}%` }} />
          ))}
          <span
            className="overview-window"
            style={{ left: `${(from / durationMs) * 100}%`, width: `${(span / durationMs) * 100}%` }}
          />
          {playheadMs !== undefined && <span className="overview-playhead" style={{ left: `${(playheadMs / durationMs) * 100}%` }} />}
        </div>
      )}
    </figure>
  );
}

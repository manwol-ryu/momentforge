import { memo, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import type { Candidate, DetectWindow, Gap, TimelineSpan } from "../../api/types";
import { strengthLabel } from "../candidates/strength";
import { tickStep, timecode } from "../format";
import { columnsOf, type Column } from "./columns";

/** What a bar's height shows: messages in its window, or how unusual the window is for its time in the broadcast (window scores). */
export type TimelineScale = "count" | "score";

export interface ExtraMarker {
  id: string;
  ms: number;
  kind: "viewer_comment" | "ai";
  label: string;
}

interface Props {
  durationMs: number;
  windows: DetectWindow[];
  /** The candidates to mark (the list's filters already applied). */
  candidates: Candidate[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  /** Candidate numbers by ID, as the list shows them ("후보 3"); their place in candidates when absent. */
  numbers?: Map<string, number>;
  /** Step between evaluation times; each window's bar covers one step. */
  stepMs?: number;
  /** Length of the span each bar counts (VOD: 10 s windows every 5 s). */
  windowMs?: number;
  /** count (default): messages per window, with the usual level as a line. score: window scores, the ones at or above scoreThreshold in the candidate color. */
  scale?: TimelineScale;
  scoreThreshold?: number;
  /** Spans with no observed chat, shown hatched. */
  gaps?: Gap[];
  /** Moments from other sources (viewer comments, AI), drawn as their own markers. */
  extraMarkers?: ExtraMarker[];
  /** The playhead: where the video (or, without one, the chat view) is. */
  playheadMs?: number;
  /** Moves the playhead; the track becomes a slider for pointer and keyboard. */
  onScrub?: (ms: number) => void;
  /** Accessible name of that slider. */
  scrubLabel?: string;
  /** The chosen moment's cut plan: the spans of the boundary candidate shown, drawn as their own bands. */
  cutSpans?: TimelineSpan[];
  /** The part of the timeline drawn (zoomed in); the whole length when absent. */
  viewStartMs?: number;
  viewEndMs?: number;
}

const BAR_AREA = 64; // viewBox units; the track's CSS height stretches it
const SCORE_TOP = 8; // the score at the top of the track; higher ones are clipped
const NO_GAPS: Gap[] = [];
const NO_MARKERS: ExtraMarker[] = [];
const NO_SPANS: TimelineSpan[] = [];

/**
 * Reaction density over the timeline: the windows gathered into columns of
 * about a pixel (each the busiest of its windows), candidate ranges as
 * bands, and a marker per candidate at its peak, sized by its strength.
 * Markers are buttons, so the timeline is usable with a keyboard as well as
 * a pointer. With a playhead, a line shows it and a see-through slider over
 * the track moves it, like an editor's timeline. Zoomed in (a view range),
 * it draws only that part, and a thin overview under it shows where the part
 * lies in the whole length. Everything but the playhead is memoized, so a
 * playing video only moves the line.
 */
export function Timeline({
  durationMs,
  windows,
  candidates,
  selectedId,
  onSelect,
  numbers,
  stepMs = 5000,
  windowMs = 10_000,
  scale = "count",
  scoreThreshold,
  gaps = NO_GAPS,
  extraMarkers = NO_MARKERS,
  cutSpans = NO_SPANS,
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
  const longForm = durationMs >= 3_600_000;
  const at = (ms: number) => `${((Math.min(Math.max(ms, from), to) - from) / span) * 100}%`;

  // About one column per pixel of the track.
  const track = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = track.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(Math.round(el.clientWidth)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const summary = useMemo(() => {
    // The vertical scale is the whole broadcast's busiest window, so zooming does not rescale the bars.
    let maxN = 1;
    let busiest: DetectWindow | null = null;
    let top: DetectWindow | null = null;
    for (const w of windows) {
      if (!w.valid) continue;
      if (w.n > maxN) maxN = w.n;
      if (!busiest || w.n > busiest.n) busiest = w;
      if (w.score !== undefined && (!top || w.score > (top.score ?? -Infinity))) top = w;
    }
    return { maxN, busiest, top };
  }, [windows]);
  const byScore = scale === "score" && !!summary.top;

  let label = "반응 밀도 그래프";
  if (byScore && summary.top) {
    label += `, 평소 대비. ${timecode(durationMs, longForm)} 길이, 평소보다 가장 튄 곳은 ${timecode(summary.top.t - windowMs, longForm)}부터 ${
      windowMs / 1000
    }초.${scoreThreshold !== undefined ? " 편집 후보가 되는 기준을 넘은 막대는 산호색." : ""}`;
  } else {
    const b = summary.busiest;
    label += `. ${timecode(durationMs, longForm)} 길이, 가장 많은 채팅은 ${
      b ? `${timecode(b.t - windowMs, longForm)}부터 ${windowMs / 1000}초 동안 ${b.n}건` : "없음"
    }.`;
  }
  if (gaps.length > 0) label += ` 채팅을 받지 못한 구간 ${gaps.length}개.`;
  if (zoomed) label += ` 지금 ${timecode(from, longForm)}부터 ${timecode(to, longForm)}까지 확대해 보는 중.`;

  return (
    <figure className="timeline" data-zoomed={zoomed || undefined}>
      <div className="timeline-track" ref={track}>
        <Bars
          windows={windows}
          from={from}
          to={to}
          stepMs={stepMs}
          columns={width || 800}
          byScore={byScore}
          scoreThreshold={scoreThreshold}
          maxN={summary.maxN}
          label={label}
        />
        <Spans gaps={gaps} candidates={candidates} cutSpans={cutSpans} selectedId={selectedId} from={from} to={to} longForm={longForm} />
        {playheadMs !== undefined && playheadMs >= from && playheadMs <= to && (
          <div className="timeline-playhead" style={{ left: at(playheadMs) }} aria-hidden="true" />
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
      <Markers
        candidates={candidates}
        extraMarkers={extraMarkers}
        numbers={numbers}
        selectedId={selectedId}
        onSelect={onSelect}
        from={from}
        to={to}
        longForm={longForm}
      />
      <Axis from={from} to={to} longForm={longForm} width={width || 800} />
      {zoomed && (
        <div className="timeline-overview" aria-hidden="true">
          {candidates.map((c) => (
            <span key={c.id} className="overview-mark" data-strength={c.strength} style={{ left: `${(c.peakMs / durationMs) * 100}%` }} />
          ))}
          <span className="overview-window" style={{ left: `${(from / durationMs) * 100}%`, width: `${(span / durationMs) * 100}%` }} />
          {playheadMs !== undefined && <span className="overview-playhead" style={{ left: `${(playheadMs / durationMs) * 100}%` }} />}
        </div>
      )}
    </figure>
  );
}

interface BarsProps {
  windows: DetectWindow[];
  from: number;
  to: number;
  stepMs: number;
  columns: number;
  byScore: boolean;
  scoreThreshold?: number;
  maxN: number;
  label: string;
}

/**
 * The bars as three paths (ordinary, past the candidate threshold, no chat
 * observed) instead of a rect per window: a long broadcast has thousands of
 * windows. In count mode a line shows the usual level of each column; in
 * score mode a dashed line shows the candidate threshold.
 */
const Bars = memo(function Bars({ windows, from, to, stepMs, columns, byScore, scoreThreshold, maxN, label }: BarsProps) {
  const cols = useMemo(() => columnsOf(windows, from, to, stepMs, columns), [windows, from, to, stepMs, columns]);
  const paths = useMemo(() => {
    let plain = "";
    let hot = "";
    let invalid = "";
    let usual = "";
    let drawing = false;
    const rect = (c: Column, h: number) => `M${c.x} ${BAR_AREA}V${BAR_AREA - h}h${c.w}V${BAR_AREA}Z`;
    for (const c of cols) {
      if (!c.valid) {
        invalid += rect(c, 3);
        drawing = false;
        continue;
      }
      if (byScore) {
        const s = c.score ?? 0;
        const piece = rect(c, Math.max(1, (Math.min(Math.max(s, 0), SCORE_TOP) / SCORE_TOP) * BAR_AREA));
        if (scoreThreshold !== undefined && s >= scoreThreshold) hot += piece;
        else plain += piece;
        continue;
      }
      plain += rect(c, Math.max(1, (c.n / maxN) * BAR_AREA));
      if (c.usual === undefined) {
        drawing = false;
        continue;
      }
      const y = BAR_AREA - Math.min(c.usual / maxN, 1) * BAR_AREA;
      usual += `${drawing ? "L" : "M"}${c.x} ${y}H${c.x + c.w}`;
      drawing = true;
    }
    return { plain, hot, invalid, usual };
  }, [cols, byScore, scoreThreshold, maxN]);
  const thresholdY =
    byScore && scoreThreshold !== undefined ? BAR_AREA - (Math.min(Math.max(scoreThreshold, 0), SCORE_TOP) / SCORE_TOP) * BAR_AREA : null;
  return (
    <svg
      className="timeline-bars"
      viewBox={`${from} 0 ${to - from} ${BAR_AREA}`}
      preserveAspectRatio="none"
      role="img"
      aria-label={label}
      data-scale={byScore ? "score" : "count"}
    >
      {paths.plain && <path className="bar" d={paths.plain} />}
      {paths.hot && <path className="bar bar-hot" d={paths.hot} />}
      {paths.invalid && <path className="bar bar-invalid" d={paths.invalid} />}
      {paths.usual && <path className="bar-usual" d={paths.usual} vectorEffect="non-scaling-stroke" />}
      {thresholdY !== null && (
        <line className="bar-threshold" x1={from} x2={to} y1={thresholdY} y2={thresholdY} vectorEffect="non-scaling-stroke" />
      )}
    </svg>
  );
});

interface SpansProps {
  gaps: Gap[];
  candidates: Candidate[];
  cutSpans: TimelineSpan[];
  selectedId: string | null;
  from: number;
  to: number;
  longForm: boolean;
}

/** Hatched gaps, candidate ranges and the chosen cut plan's spans over the bars. */
const Spans = memo(function Spans({ gaps, candidates, cutSpans, selectedId, from, to, longForm }: SpansProps) {
  const span = to - from;
  const overlaps = (a: number, b: number) => b >= from && a <= to;
  const left = (ms: number) => `${((Math.min(Math.max(ms, from), to) - from) / span) * 100}%`;
  const width = (a: number, b: number) => `${((Math.min(b, to) - Math.max(a, from)) / span) * 100}%`;
  return (
    <>
      {gaps
        .filter((g) => overlaps(g.startMs, g.endMs))
        .map((g) => (
          <div key={g.startMs} className="timeline-gap" style={{ left: left(g.startMs), width: width(g.startMs, g.endMs) }} />
        ))}
      {candidates
        .filter((c) => overlaps(c.reactionStartMs, c.reactionEndMs))
        .map((c) => (
          <div
            key={c.id}
            className="timeline-band"
            data-selected={c.id === selectedId || undefined}
            data-strength={c.strength}
            style={{ left: left(c.reactionStartMs), width: width(c.reactionStartMs, c.reactionEndMs) }}
          />
        ))}
      {cutSpans
        .filter((sp) => overlaps(sp.startMs, sp.endMs))
        .map((sp) => (
          <div
            key={`${sp.startMs}-${sp.endMs}`}
            className="timeline-cut"
            data-role={sp.role}
            title={`AI 컷 구간 ${timecode(sp.startMs, longForm)}–${timecode(sp.endMs, longForm)}`}
            style={{ left: left(sp.startMs), width: width(sp.startMs, sp.endMs) }}
          />
        ))}
    </>
  );
});

interface MarkersProps {
  candidates: Candidate[];
  extraMarkers: ExtraMarker[];
  numbers?: Map<string, number>;
  selectedId: string | null;
  onSelect: (id: string) => void;
  from: number;
  to: number;
  longForm: boolean;
}

/** A button per moment in view: candidates as triangles sized by strength, viewer picks as diamonds, AI picks as circles. */
const Markers = memo(function Markers({ candidates, extraMarkers, numbers, selectedId, onSelect, from, to, longForm }: MarkersProps) {
  const span = to - from;
  const inView = (ms: number) => ms >= from && ms <= to;
  const place = (ms: number) => ({ left: `${((ms - from) / span) * 100}%`, "--at": (ms - from) / span }) as CSSProperties;
  return (
    <div className="timeline-markers">
      {candidates.map((c, i) => {
        if (!inView(c.peakMs)) return null;
        const words = [`후보 ${numbers?.get(c.id) ?? i + 1}`, timecode(c.reactionStartMs, longForm)];
        if (c.strength) words.push(strengthLabel[c.strength]);
        if (c.reasons.length) words.push(c.reasons.join(", "));
        return (
          <button
            key={c.id}
            type="button"
            className="timeline-marker"
            data-strength={c.strength}
            style={place(c.peakMs)}
            aria-pressed={c.id === selectedId}
            aria-label={words.join(", ")}
            title={words.join(" · ")}
            onClick={() => onSelect(c.id)}
          />
        );
      })}
      {extraMarkers
        .filter((m) => inView(m.ms))
        .map((m) => (
          <button
            key={m.id}
            type="button"
            className="timeline-marker"
            data-kind={m.kind}
            style={place(m.ms)}
            aria-pressed={m.id === selectedId}
            aria-label={m.label}
            title={m.label}
            onClick={() => onSelect(m.id)}
          />
        ))}
    </div>
  );
});

/** Time labels at round steps across the drawn range, about one per 72 pixels so they never run into each other. */
const Axis = memo(function Axis({ from, to, longForm, width }: { from: number; to: number; longForm: boolean; width: number }) {
  const span = to - from;
  const tick = tickStep(span, Math.max(2, Math.min(12, Math.floor(width / 72))));
  const ticks: number[] = [];
  for (let t = Math.ceil(from / tick) * tick; t <= to; t += tick) ticks.push(t);
  return (
    <div className="timeline-axis" aria-hidden="true">
      {ticks.map((t) => {
        const at = (t - from) / span;
        return (
          <span key={t} className="timecode" data-edge={at < 0.04 ? "start" : at > 0.96 ? "end" : undefined} style={{ left: `${at * 100}%` }}>
            {timecode(t, longForm)}
          </span>
        );
      })}
    </div>
  );
});

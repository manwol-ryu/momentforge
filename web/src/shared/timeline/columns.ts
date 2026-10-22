import type { DetectWindow } from "../../api/types";

/** One drawn bar: its span in timeline ms and what it shows. */
export interface Column {
  x: number;
  w: number;
  /** The busiest window's messages, and the highest window score. */
  n: number;
  score?: number;
  /** The usual messages per window there (the windows' mean baseline). */
  usual?: number;
  /** Some chat was observed (false: every window is a gap). */
  valid: boolean;
}

/**
 * The windows in [from, to] gathered into at most `columns` equal columns
 * (about one per pixel of the track), each keeping its busiest window's
 * count and highest score, so a peak is never averaged away. When the view
 * is zoomed in so far that a window is wider than a column, every window
 * stays its own bar.
 */
export function columnsOf(windows: readonly DetectWindow[], from: number, to: number, stepMs: number, columns: number): Column[] {
  const colMs = (to - from) / Math.max(1, Math.floor(columns));
  const out: Column[] = [];
  if (colMs <= stepMs) {
    for (const w of windows) {
      if (w.t < from || w.t - stepMs > to) continue;
      out.push({ x: w.t - stepMs, w: stepMs, n: w.valid ? w.n : 0, score: w.valid ? w.score : undefined, usual: w.baselineN, valid: w.valid });
    }
    return out;
  }
  const last = Math.max(0, Math.floor(columns) - 1);
  let cur: Column | null = null;
  let at = -1;
  let usualSum = 0;
  let usualCount = 0;
  const close = () => {
    if (!cur) return;
    if (usualCount > 0) cur.usual = usualSum / usualCount;
    out.push(cur);
  };
  // Windows come in time order, so each column is filled in one run.
  for (const w of windows) {
    if (w.t < from || w.t - stepMs > to) continue;
    const i = Math.min(last, Math.max(0, Math.floor((w.t - stepMs / 2 - from) / colMs)));
    if (i !== at || !cur) {
      close();
      cur = { x: from + i * colMs, w: colMs, n: 0, valid: false };
      at = i;
      usualSum = 0;
      usualCount = 0;
    }
    if (w.baselineN !== undefined) {
      usualSum += w.baselineN;
      usualCount++;
    }
    if (!w.valid) continue;
    cur.valid = true;
    if (w.n > cur.n) cur.n = w.n;
    if (w.score !== undefined && (cur.score === undefined || w.score > cur.score)) cur.score = w.score;
  }
  close();
  return out;
}

/** Timeline position as m:ss, or h:mm:ss when the timeline is an hour or longer. */
export function timecode(ms: number, longForm = false): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const ss = String(s).padStart(2, "0");
  if (h > 0 || longForm) return `${h}:${String(m).padStart(2, "0")}:${ss}`;
  return `${String(m).padStart(2, "0")}:${ss}`;
}

/** Tick spacing so a timeline shows at most maxLabels labels (12 by default; fewer where the track is narrow). */
export function tickStep(durationMs: number, maxLabels = 12): number {
  const steps = [60_000, 120_000, 300_000, 600_000, 1_800_000, 3_600_000, 7_200_000];
  // Past two hours a step, whole hours that keep the count (a 12-hour broadcast on a phone: every 3 hours).
  return steps.find((s) => durationMs / s <= maxLabels) ?? Math.ceil(durationMs / maxLabels / 3_600_000) * 3_600_000;
}

/** File size for download lists. */
export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

/** US dollars: cents for amounts of a cent or more, otherwise four decimals. */
export function usd(v: number): string {
  return `$${v >= 0.01 || v === 0 ? v.toFixed(2) : v.toFixed(4)}`;
}

/** Token counts with thousands separators. */
export function tokens(n: number): string {
  return n.toLocaleString("ko-KR");
}

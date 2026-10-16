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

/** Tick spacing so a timeline shows roughly 6–12 labels. */
export function tickStep(durationMs: number): number {
  const steps = [60_000, 120_000, 300_000, 600_000, 1_800_000, 3_600_000];
  return steps.find((s) => durationMs / s <= 12) ?? 3_600_000;
}

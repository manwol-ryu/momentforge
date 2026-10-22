import type { Candidate, Measure, Strength, TimelineItem } from "../../api/types";

export const strengthLabel: Record<Strength, string> = { strong: "강함", medium: "보통", weak: "약함" };

/** The list's order: video time, or the whole-broadcast rank (strongest first). */
export type SortOrder = "time" | "strength";

/** Which rule candidates the list keeps: all of them, or only the strong and medium ones. */
export type StrengthFilter = "all" | "strong_medium";

/**
 * Candidate numbers as the timeline marks them ("후보 3"): their place in
 * time order, so a candidate keeps its number however the list is sorted
 * or filtered.
 */
export function candidateNumbers(candidates: readonly Candidate[]): Map<string, number> {
  const ordered = [...candidates].sort((a, b) => a.reactionStartMs - b.reactionStartMs || a.id.localeCompare(b.id));
  return new Map(ordered.map((c, i) => [c.id, i + 1]));
}

/** Whether an analysis ranked its candidates (whole-broadcast analyses; LIVE and older ones do not). */
export function hasStrengths(candidates: Iterable<Candidate>): boolean {
  for (const c of candidates) if (c.strength) return true;
  return false;
}

/** Rule candidates per strength. */
export function strengthCounts(candidates: Iterable<Candidate>): Record<Strength, number> {
  const counts: Record<Strength, number> = { strong: 0, medium: 0, weak: 0 };
  for (const c of candidates) if (c.strength) counts[c.strength]++;
  return counts;
}

/** A row stays unless it is a weak rule candidate and only strong and medium ones are kept. */
export function keepRow(it: TimelineItem, candidates: Map<string, Candidate>, filter: StrengthFilter): boolean {
  if (filter === "all" || it.source !== "rule") return true;
  return candidates.get(it.originId)?.strength !== "weak";
}

/**
 * Rows (given in time order) in the chosen order. By strength, ranked rule
 * candidates come first (rank 1 on top) and every other row follows in its
 * time order.
 */
export function sortRows(rows: readonly TimelineItem[], candidates: Map<string, Candidate>, order: SortOrder): TimelineItem[] {
  if (order === "time") return [...rows];
  const rank = (it: TimelineItem) => (it.source === "rule" ? candidates.get(it.originId)?.rank : undefined) ?? Number.MAX_SAFE_INTEGER;
  return [...rows].sort((a, b) => rank(a) - rank(b)); // stable: unranked rows keep their order
}

/** One measure in plain words: what was counted and the unit. */
function measureName(key: string): { name: string; unit: string } {
  if (key === "laugh") return { name: "웃음", unit: "명" };
  if (key === "emotion:surprise") return { name: "놀람", unit: "명" };
  if (key === "emotion:sadness") return { name: "아쉬움", unit: "명" };
  if (key === "emotion" || key.startsWith("emotion:")) return { name: "감정 반응", unit: "명" };
  if (key.startsWith("repeat:")) {
    const text = key.slice("repeat:".length);
    return { name: `‘${text.length > 12 ? text.slice(0, 12) + "…" : text}’ 반복`, unit: "명" };
  }
  if (key === "repeat") return { name: "같은 말 반복", unit: "명" };
  if (key === "reaction") return { name: "반응한 사람", unit: "명" };
  if (key === "surge") return { name: "채팅", unit: "건" };
  if (key === "participants") return { name: "채팅한 사람", unit: "명" };
  return { name: key, unit: "건" };
}

const smallFormat = new Intl.NumberFormat("ko-KR", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const largeFormat = new Intl.NumberFormat("ko-KR", { maximumFractionDigits: 0 });

/** "웃음 7명" and "평소 0.6명" for one measure (a tenth matters only for small counts). */
export function measureParts(m: Measure): { observed: string; usual: string } {
  const { name, unit } = measureName(m.key);
  const usual =
    m.usual < 0.05 ? "평소 거의 없음" : `평소 ${(m.usual < 10 ? smallFormat : largeFormat).format(m.usual)}${unit}`;
  return { observed: `${name} ${m.observed.toLocaleString("ko-KR")}${unit}`, usual };
}

/** Reaction measures named after what viewers wrote (the "reaction" total adds little beside them). */
const specificReaction = (key: string) => key === "laugh" || key.startsWith("emotion") || key.startsWith("repeat");

/**
 * What stood out at a candidate's peak, at most three measures, strongest
 * first; the total of reacting authors is left out when a specific reaction
 * (laugh, emotion, a repeated phrase) is there to name it.
 */
export function topMeasures(measures: readonly Measure[] | undefined, limit = 3): Measure[] {
  if (!measures?.length) return [];
  const named = measures.some((m) => specificReaction(m.key));
  return measures.filter((m) => !(named && m.key === "reaction")).slice(0, limit);
}


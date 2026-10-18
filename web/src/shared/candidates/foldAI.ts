import type { TimelineItem } from "../../api/types";

export interface FoldedItems {
  /** The rows of the list: every non-AI item, and the AI items that explain none of them. */
  rows: TimelineItem[];
  /** AI explanations by the ID of the row they belong under. */
  aiFor: Map<string, TimelineItem[]>;
}

/**
 * An AI recommendation that explains a listed candidate (its
 * sourceCandidateIds name it) is shown under that candidate instead of as a
 * row of its own; one that found a new moment stays a row.
 */
export function foldAI(items: TimelineItem[]): FoldedItems {
  const listed = new Set(items.filter((i) => i.source !== "ai").map((i) => i.id));
  const rows: TimelineItem[] = [];
  const aiFor = new Map<string, TimelineItem[]>();
  for (const it of items) {
    const hosts = it.source === "ai" ? (it.sourceCandidateIds ?? []).filter((id) => listed.has(id)) : [];
    if (hosts.length === 0) {
      rows.push(it);
      continue;
    }
    for (const host of hosts) aiFor.set(host, [...(aiFor.get(host) ?? []), it]);
  }
  return { rows, aiFor };
}

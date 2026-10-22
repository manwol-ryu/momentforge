import { useEffect, useMemo, useState } from "react";

/** A cut box's "다시 분석": that candidate alone, continuing its plan (its versions and notes). */
export interface Reanalysis {
  itemId: string;
  planId?: string;
  /** How the candidate list names it ("후보 3, 07:25"). */
  label: string;
}

/**
 * What the candidate list hands the AI review: the candidates ticked for it,
 * and a cut box's request to analyse one candidate again. The re-analysis
 * stands apart from the ticks, so asking for it never drops what the user
 * picked; asking to review the picks ends it. Both start over when the
 * analysis changes (candidate IDs follow time, so they would name other
 * moments).
 */
export function useAIReview(resetKey: unknown) {
  const [selection, setSelection] = useState<Set<string>>(() => new Set());
  const [reanalysis, setReanalysis] = useState<Reanalysis | null>(null);
  // Counts the asks; the AI tab previews each one.
  const [asked, setAsked] = useState(0);
  useEffect(() => {
    setSelection(new Set());
    setReanalysis(null);
  }, [resetKey]);

  const toggle = (itemId: string) =>
    setSelection((cur) => {
      const next = new Set(cur);
      if (!next.delete(itemId)) next.add(itemId);
      return next;
    });
  const picked = useMemo(() => [...selection], [selection]);
  const pick = (itemIds: string[]) => setSelection((cur) => new Set([...cur, ...itemIds]));
  const clear = () => setSelection(new Set());
  /** Review the ticked candidates (the pick bar's "AI 검토로 이동"). */
  const askPicks = () => {
    setReanalysis(null);
    setAsked((n) => n + 1);
  };
  /** Analyse one candidate's cut again (a cut box's "다시 분석"). */
  const askReanalysis = (r: Reanalysis) => {
    setReanalysis(r);
    setAsked((n) => n + 1);
  };
  return {
    selection,
    picked,
    toggle,
    pick,
    clear,
    reanalysis,
    endReanalysis: () => setReanalysis(null),
    asked,
    askPicks,
    askReanalysis,
  };
}

export type AIReview = ReturnType<typeof useAIReview>;

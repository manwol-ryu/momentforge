import { useMemo, useState } from "react";
import type { CutBoundaryName, TimelineItem } from "../../api/types";
import { boundarySpans, startBoundary } from "./CutBox";

/**
 * Which boundary candidate each cut box shows, and the spans of the chosen
 * row's cut analysis for the timeline: the box last switched, else the
 * confirmed one, else the newest.
 */
export function useCutView(items: TimelineItem[], aiFor: Map<string, TimelineItem[]>, selectedId: string | null) {
  const [choice, setChoice] = useState<Record<string, CutBoundaryName>>({});
  const [focus, setFocus] = useState<string | null>(null);
  const choose = (planId: string, name: CutBoundaryName) => {
    setChoice((cur) => ({ ...cur, [planId]: name }));
    setFocus(planId);
  };
  const selectedRow = selectedId ? items.find((i) => i.id === selectedId) : undefined;
  const here = [...((selectedId && aiFor.get(selectedId)) || []), ...(selectedRow?.kind === "cut" ? [selectedRow] : [])].filter(
    (n) => n.kind === "cut" && n.cut,
  );
  const shown = here.find((n) => n.cut!.planId === focus) ?? here.find((n) => n.cut!.confirmed) ?? here[here.length - 1];
  const spans = useMemo(
    () => (shown ? boundarySpans(shown, choice[shown.cut!.planId] ?? startBoundary(shown.cut!)) : []),
    [shown, choice],
  );
  return { choice, choose, spans };
}

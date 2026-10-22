import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { AIRun } from "../../api/types";
import { useEvents } from "../events";

/**
 * Keeps a session's AI runs fresh from the "ai" server events: each run's
 * progress, the session's run list, and, when a run ends, what it changed
 * (the timeline's explanations and cut analyses, a fixed transcript, the
 * exported files). A LIVE capture's "transcript" events (its candidates'
 * ranges transcribed) refresh the transcript too.
 */
export function useAIEvents(sessionId: string) {
  const queryClient = useQueryClient();
  const { subscribe } = useEvents();
  useEffect(
    () =>
      subscribe("transcript", (event) => {
        if ((event.data as { sessionId: string }).sessionId !== sessionId) return;
        void queryClient.invalidateQueries({ queryKey: ["transcript", sessionId] });
        void queryClient.invalidateQueries({ queryKey: ["utterances", sessionId] });
        void queryClient.invalidateQueries({ queryKey: ["liveFiles", sessionId] });
      }),
    [subscribe, queryClient, sessionId],
  );
  useEffect(
    () =>
      subscribe("ai", (event) => {
        const r = event.data as AIRun;
        if (r.sessionId !== sessionId) return;
        queryClient.setQueryData(["aiRun", r.id], r);
        void queryClient.invalidateQueries({ queryKey: ["aiRuns", sessionId] });
        if (r.state === "running") return;
        void queryClient.invalidateQueries({ queryKey: ["timeline", sessionId] });
        void queryClient.invalidateQueries({ queryKey: ["files", sessionId] });
        void queryClient.invalidateQueries({ queryKey: ["liveFiles", sessionId] });
        if (r.kind === "transcript") {
          void queryClient.invalidateQueries({ queryKey: ["transcript", sessionId] });
          void queryClient.invalidateQueries({ queryKey: ["utterances", sessionId] });
        }
      }),
    [subscribe, queryClient, sessionId],
  );
}

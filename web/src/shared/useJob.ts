import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import type { JobStatus } from "../api/types";
import { useEvents } from "./events";

/** Whether a is a later state of the same job than b (a finished job never runs again). */
function later(a: JobStatus, b: JobStatus): boolean {
  return a.id === b.id && ((!a.busy && b.busy) || Date.parse(a.updatedAt) > Date.parse(b.updatedAt));
}

/**
 * The current background job. Loaded once, then kept fresh by "job" server
 * events. A job's new session appears in the saved list as soon as it is
 * made, and when a job stops, session data is refetched.
 */
export function useJob() {
  const queryClient = useQueryClient();
  const { subscribe } = useEvents();
  const job = useQuery({
    queryKey: ["job"],
    // An event may bring a later state of the job while this request is out
    // (a job that ends within milliseconds): an answer must not undo it.
    queryFn: async () => {
      const fresh = await api.job();
      const known = queryClient.getQueryData<JobStatus>(["job"]);
      return known && fresh && later(known, fresh) ? known : fresh;
    },
  });
  const wasBusy = useRef(false);
  const lastSession = useRef<string | undefined>(undefined);
  const lastPhase = useRef<string | undefined>(undefined);

  useEffect(
    () =>
      subscribe("job", (event) => {
        const status = event.data as JobStatus;
        queryClient.setQueryData(["job"], status);
        if (status.busy && status.sessionId && status.sessionId !== lastSession.current) {
          void queryClient.invalidateQueries({ queryKey: ["sessions"] });
        }
        lastSession.current = status.sessionId || undefined;
        // A job that moves on to a later phase (chat saved → candidates found →
        // transcribing) has finished writing what the earlier phase produced,
        // so the open session shows it without waiting for the whole job.
        if (status.busy && status.sessionId && lastPhase.current && status.phase !== lastPhase.current) {
          void queryClient.invalidateQueries({ queryKey: ["sessions"] });
          void queryClient.invalidateQueries({ queryKey: ["session", status.sessionId] });
          void queryClient.invalidateQueries({ queryKey: ["timeline", status.sessionId] });
        }
        lastPhase.current = status.busy ? status.phase : undefined;
        if (wasBusy.current && !status.busy) {
          void queryClient.invalidateQueries({ queryKey: ["sessions"] });
          void queryClient.invalidateQueries({ queryKey: ["session"] });
          void queryClient.invalidateQueries({ queryKey: ["comments"] });
          void queryClient.invalidateQueries({ queryKey: ["timeline"] });
          void queryClient.invalidateQueries({ queryKey: ["transcript"] });
          void queryClient.invalidateQueries({ queryKey: ["utterances"] });
          void queryClient.invalidateQueries({ queryKey: ["files"] });
        }
        wasBusy.current = status.busy;
      }),
    [subscribe, queryClient],
  );
  return job;
}

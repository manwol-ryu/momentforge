import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import type { JobStatus } from "../api/types";
import { useEvents } from "./events";

/**
 * The current background job. Loaded once, then kept fresh by "job" server
 * events; when a job stops, session data is refetched.
 */
export function useJob() {
  const queryClient = useQueryClient();
  const { subscribe } = useEvents();
  const job = useQuery({ queryKey: ["job"], queryFn: api.job });
  const wasBusy = useRef(false);

  useEffect(
    () =>
      subscribe("job", (event) => {
        const status = event.data as JobStatus;
        queryClient.setQueryData(["job"], status);
        if (wasBusy.current && !status.busy) {
          void queryClient.invalidateQueries({ queryKey: ["sessions"] });
          void queryClient.invalidateQueries({ queryKey: ["session"] });
          void queryClient.invalidateQueries({ queryKey: ["comments"] });
          void queryClient.invalidateQueries({ queryKey: ["timeline"] });
        }
        wasBusy.current = status.busy;
      }),
    [subscribe, queryClient],
  );
  return job;
}

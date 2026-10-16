import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../api/client";
import type { ServerEvent } from "../api/types";

export type ConnectionState = "connecting" | "open" | "closed";
type Listener = (event: ServerEvent) => void;

interface EventsContext {
  state: ConnectionState;
  subscribe: (type: string, fn: Listener) => () => void;
}

const Ctx = createContext<EventsContext | null>(null);

/**
 * Keeps one SSE connection to /api/events. It uses fetch instead of
 * EventSource so the access key travels in a header, not the URL.
 * On disconnect it retries with backoff; on "overflow" it refetches all queries.
 */
export function EventsProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [state, setState] = useState<ConnectionState>("connecting");
  const listeners = useRef(new Map<string, Set<Listener>>());

  useEffect(() => {
    const abort = new AbortController();
    let retry = 1000;
    let timer: number | undefined;

    const dispatch = (event: ServerEvent) => {
      if (event.type === "overflow") void queryClient.invalidateQueries();
      listeners.current.get(event.type)?.forEach((fn) => fn(event));
    };

    const connect = async () => {
      setState("connecting");
      try {
        const res = await apiFetch("/api/events", { signal: abort.signal });
        const reader = res.body!.pipeThrough(new TextDecoderStream()).getReader();
        let buffer = "";
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += value;
          let end: number;
          while ((end = buffer.indexOf("\n\n")) >= 0) {
            const frame = buffer.slice(0, end);
            buffer = buffer.slice(end + 2);
            const event = parseFrame(frame);
            if (!event) continue;
            if (event.type === "ready") {
              retry = 1000;
              setState("open");
            }
            dispatch(event);
          }
        }
      } catch {
        // fall through to reconnect
      }
      if (abort.signal.aborted) return;
      setState("closed");
      timer = window.setTimeout(connect, retry);
      retry = Math.min(retry * 2, 10_000);
    };

    void connect();
    return () => {
      abort.abort();
      window.clearTimeout(timer);
    };
  }, [queryClient]);

  const subscribe = useCallback((type: string, fn: Listener) => {
    const set = listeners.current.get(type) ?? new Set<Listener>();
    set.add(fn);
    listeners.current.set(type, set);
    return () => {
      set.delete(fn);
    };
  }, []);
  const value = useMemo(() => ({ state, subscribe }), [state, subscribe]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

/** Connection state and a subscribe function for server events. */
export function useEvents(): EventsContext {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useEvents must be used inside EventsProvider");
  return ctx;
}

function parseFrame(frame: string): ServerEvent | null {
  let type = "message";
  let data = "";
  for (const line of frame.split("\n")) {
    if (line.startsWith(":")) continue; // heartbeat comment
    if (line.startsWith("event: ")) type = line.slice(7);
    else if (line.startsWith("data: ")) data += line.slice(6);
  }
  if (!data) return null;
  try {
    return { ...(JSON.parse(data) as ServerEvent), type };
  } catch {
    return null;
  }
}

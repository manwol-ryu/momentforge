import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Candidate, LiveChat, LiveUpdate } from "../../api/types";
import { useEvents } from "../../shared/events";

/** Messages kept in memory for the live log. */
const KEEP = 500;
/** A "live" event carries at most this many messages; more means fetch the rest. */
const EVENT_CHATS = 200;

/**
 * Marks the session list stale whenever a LIVE capture changes state, on
 * any screen, so the list is right when Home opens again.
 */
export function useLiveSessionUpdates() {
  const queryClient = useQueryClient();
  const { subscribe } = useEvents();
  const known = useRef(new Map<string, string>());
  useEffect(
    () =>
      subscribe("live", (event) => {
        const u = event.data as LiveUpdate;
        if (known.current.get(u.sessionId) === u.state) return;
        known.current.set(u.sessionId, u.state);
        void queryClient.invalidateQueries({ queryKey: ["sessions"] });
      }),
    [subscribe, queryClient],
  );
}

/**
 * Recent chat and the candidates of one LIVE capture. Loads once, then
 * follows "live" server events: new chat comes in the event, and a higher
 * candidate revision fetches only the changed candidates. On reconnect or
 * overflow it catches up from the last seen message and revision.
 */
export function useLiveFeed(id: string) {
  const queryClient = useQueryClient();
  const { subscribe } = useEvents();
  const [chats, setChats] = useState<LiveChat[]>([]);
  // Every message taken in so far; only the last KEEP stay in memory, so the log counts what arrived from this.
  const [received, setReceived] = useState(0);
  const [candidates, setCandidates] = useState<Map<string, Candidate>>(new Map());
  const [error, setError] = useState<Error | null>(null);
  const [loaded, setLoaded] = useState(false);
  const lastSeq = useRef(0);
  const revision = useRef(0);
  const syncing = useRef(false);
  const again = useRef(false);

  const append = useCallback((rows: LiveChat[]) => {
    const fresh = rows.filter((c) => c.seq > lastSeq.current);
    if (fresh.length === 0) return;
    lastSeq.current = fresh[fresh.length - 1]!.seq;
    setChats((prev) => [...prev, ...fresh].slice(-KEEP));
    setReceived((n) => n + fresh.length);
  }, []);

  // One catch-up at a time; a request made meanwhile runs once more after it.
  const sync = useCallback(() => {
    if (syncing.current) {
      again.current = true;
      return;
    }
    syncing.current = true;
    void (async () => {
      try {
        do {
          again.current = false;
          const r = await api.liveChats(id, lastSeq.current);
          append(r.chats);
          const c = await api.liveCandidates(id, revision.current);
          revision.current = c.revision;
          if (c.candidates.length > 0) {
            setCandidates((prev) => {
              const next = new Map(prev);
              for (const cand of c.candidates) next.set(cand.id, cand);
              return next;
            });
          }
        } while (again.current);
        setError(null);
        setLoaded(true);
      } catch (e) {
        setError(e as Error);
      } finally {
        syncing.current = false;
      }
    })();
  }, [id, append]);

  useEffect(() => {
    sync();
    const offLive = subscribe("live", (event) => {
      const u = event.data as LiveUpdate;
      if (u.sessionId !== id) return;
      void queryClient.invalidateQueries({ queryKey: ["live", id] });
      if (syncing.current || u.chats.length >= EVENT_CHATS) sync();
      else append(u.chats);
      if (u.revision > revision.current) sync();
    });
    const resync = () => {
      sync();
      void queryClient.invalidateQueries({ queryKey: ["live", id] });
    };
    const offReady = subscribe("ready", resync);
    const offOverflow = subscribe("overflow", resync);
    return () => {
      offLive();
      offReady();
      offOverflow();
    };
  }, [id, subscribe, sync, append, queryClient]);

  const sorted = useMemo(() => [...candidates.values()].sort((a, b) => a.clipStartMs - b.clipStartMs), [candidates]);
  return { chats, received, candidates: sorted, error, loaded, retry: sync };
}

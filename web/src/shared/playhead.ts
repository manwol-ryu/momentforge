import { useSyncExternalStore } from "react";

/**
 * The playhead kept outside React state. The video reports its time several
 * times a second; keeping it here means only what shows the time (the
 * playhead line, the clock, the chat at the playhead) re-renders, not the
 * candidate list or the tool tabs around it.
 */
export interface Playhead {
  get(): number;
  set(ms: number): void;
  subscribe(listener: () => void): () => void;
}

export function createPlayhead(initialMs = 0): Playhead {
  let now = initialMs;
  const listeners = new Set<() => void>();
  return {
    get: () => now,
    set: (ms) => {
      if (ms === now) return;
      now = ms;
      for (const l of listeners) l();
    },
    subscribe: (l) => {
      listeners.add(l);
      return () => {
        listeners.delete(l);
      };
    },
  };
}

/**
 * A value derived from the playhead; the component re-renders only when the
 * value changes. select must return a primitive (a new object each call
 * would re-render every time).
 */
export function usePlayhead<T extends string | number | boolean>(p: Playhead, select: (ms: number) => T): T {
  return useSyncExternalStore(p.subscribe, () => select(p.get()));
}

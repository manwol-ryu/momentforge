import { useState } from "react";

function read<T extends string>(key: string, options: readonly T[], fallback: T): T {
  try {
    const saved = localStorage.getItem(key);
    return options.find((o) => o === saved) ?? fallback;
  } catch {
    return fallback; // storage blocked
  }
}

/**
 * A choice of one remembered in this browser (a list's order, a chart's
 * scale). Anything else stored under the key, or blocked storage, gives the
 * fallback; a blocked write lasts until reload.
 */
export function useStoredChoice<T extends string>(key: string, options: readonly T[], fallback: T): [T, (value: T) => void] {
  const [value, setValue] = useState<T>(() => read(key, options, fallback));
  const choose = (next: T) => {
    setValue(next);
    try {
      localStorage.setItem(key, next);
    } catch {
      // storage blocked: the choice lasts until reload
    }
  };
  return [value, choose];
}

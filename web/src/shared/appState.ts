import { useEffect } from "react";

/**
 * Publishes the page's load state on <body data-app-state>. E2E tests and
 * other tools wait for "ready" instead of network idle (SSE stays open).
 */
export function useAppState(state: "loading" | "ready" | "error") {
  useEffect(() => {
    document.body.dataset.appState = state;
  }, [state]);
}

export function queryState(q: { isSuccess: boolean; isError: boolean }): "loading" | "ready" | "error" {
  return q.isSuccess ? "ready" : q.isError ? "error" : "loading";
}

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "./api/client";
import { Shell } from "./app/Shell";
import { EventsProvider } from "./shared/events";
import { usePath } from "./shared/navigation";
import { Home } from "./features/home/Home";
import { LiveView } from "./features/live/LiveView";
import { useLiveSessionUpdates } from "./features/live/useLiveFeed";
import { SessionView } from "./features/session/SessionView";
import "./styles/app.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Server events tell us when data changes; no background polling.
      refetchOnWindowFocus: false,
      staleTime: Infinity,
      retry: (count, error) => !(error instanceof ApiError && error.status > 0 && error.status < 500) && count < 2,
    },
  },
});

function App() {
  const path = usePath();
  useLiveSessionUpdates();
  return (
    <Shell path={path}>
      <Page path={path} />
    </Shell>
  );
}

function Page({ path }: { path: string }) {
  const session = /^\/sessions\/([0-9a-f]{32})$/.exec(path);
  if (session) return <SessionView key={session[1]} id={session[1]!} />;
  const live = /^\/live\/([0-9a-f]{32})$/.exec(path);
  if (live) return <LiveView key={live[1]} id={live[1]!} />;
  return <Home />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <EventsProvider>
        <App />
      </EventsProvider>
    </QueryClientProvider>
  </StrictMode>,
);

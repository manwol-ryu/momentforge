import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "./api/client";
import { EventsProvider } from "./shared/events";
import { usePath } from "./shared/navigation";
import { Home } from "./features/home/Home";
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
  const session = /^\/sessions\/([0-9a-f]{32})$/.exec(path);
  if (session) return <SessionView key={session[1]} id={session[1]!} />;
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

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "./api/client";
import { EventsProvider } from "./shared/events";
import { Home } from "./features/home/Home";
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

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <EventsProvider>
        <Home />
      </EventsProvider>
    </QueryClientProvider>
  </StrictMode>,
);

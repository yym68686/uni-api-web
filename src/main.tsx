import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MotionConfig, LazyMotion, domAnimation } from "motion/react";
import * as Tooltip from "@radix-ui/react-tooltip";
// System fonts keep cold navigation independent of additional font downloads.
import "./styles.css";
import App from "./App";
import { PageBoundary } from "./PageBoundary";
const client = new QueryClient({
  defaultOptions: {
    queries: {
      retry: false,
      refetchOnWindowFocus: false,
      staleTime: 30_000,
      gcTime: 300_000,
    },
  },
});
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={client}>
      <MotionConfig reducedMotion="user">
        <LazyMotion features={domAnimation}>
          <Tooltip.Provider delayDuration={180}>
            <PageBoundary><App /></PageBoundary>
          </Tooltip.Provider>
        </LazyMotion>
      </MotionConfig>
    </QueryClientProvider>
  </React.StrictMode>,
);

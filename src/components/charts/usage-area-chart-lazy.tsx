"use client";

import * as React from "react";
import dynamic from "next/dynamic";

import type { DailyUsagePoint } from "@/lib/types";
import { cn } from "@/lib/utils";

interface UsageAreaChartLazyProps {
  data: DailyUsagePoint[];
  className?: string;
}

const UsageAreaChartLazyInner = dynamic(
  () => import("@/components/charts/usage-area-chart").then((m) => m.UsageAreaChart),
  {
    ssr: false,
    loading: () => <ChartPlaceholder />
  }
);

function ChartPlaceholder({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "h-72 w-full rounded-xl border border-border bg-muted/10",
        "animate-pulse",
        className
      )}
    />
  );
}

export function UsageAreaChartLazy({ data, className }: UsageAreaChartLazyProps) {
  const ref = React.useRef<HTMLDivElement | null>(null);
  const [enabled, setEnabled] = React.useState(false);

  React.useEffect(() => {
    const el = ref.current;
    if (!el) {
      setEnabled(true);
      return;
    }
    if (typeof window === "undefined" || !("IntersectionObserver" in window)) {
      setEnabled(true);
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (!entry?.isIntersecting) return;
        setEnabled(true);
        observer.disconnect();
      },
      { rootMargin: "240px" }
    );

    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  return (
    <div ref={ref} className="w-full">
      {enabled ? (
        <UsageAreaChartLazyInner data={data} className={className} />
      ) : (
        <ChartPlaceholder className={className} />
      )}
    </div>
  );
}

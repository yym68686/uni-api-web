"use client";

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
    loading: () => (
      <div
        className={cn(
          "h-72 w-full rounded-xl border border-border bg-muted/10",
          "animate-pulse"
        )}
      />
    )
  }
);

export function UsageAreaChartLazy({ data, className }: UsageAreaChartLazyProps) {
  return <UsageAreaChartLazyInner data={data} className={className} />;
}


"use client";

import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

interface BillingSkeletonProps {
  topupEnabled?: boolean;
}

export function BillingContentSkeleton({ topupEnabled = true }: BillingSkeletonProps) {
  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div
          className={cn(
            "uai-cq uai-kpi-card relative overflow-hidden rounded-xl border border-border bg-card p-6",
            topupEnabled ? "lg:col-span-2" : "sm:col-span-2 lg:col-span-4"
          )}
        >
          <div className="uai-kpi-head flex items-center justify-between">
            <Skeleton className="h-4 w-28" />
            <div className="uai-kpi-icon flex h-10 w-10 items-center justify-center rounded-xl border border-border bg-background/40">
              <Skeleton className="h-4 w-4 rounded-md bg-muted/50" />
            </div>
          </div>
          <div className="mt-4 space-y-2">
            <Skeleton className="h-7 w-44" />
            <Skeleton className="h-3.5 w-36" />
          </div>
          <div className="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-primary/10 blur-2xl opacity-70" />
        </div>

        {topupEnabled ? (
          <Card className="lg:col-span-2">
            <CardHeader className="flex flex-row items-start justify-between gap-3">
              <div className="space-y-1">
                <Skeleton className="h-5 w-36" />
                <Skeleton className="h-4 w-72 max-w-full" />
              </div>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-3">
                <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
                  <div className="order-1 space-y-2">
                    <Skeleton className="h-4 w-36" />
                    <Skeleton className="h-10 w-full rounded-xl" />
                  </div>
                  <Skeleton className="order-3 h-10 w-full rounded-xl sm:order-2 sm:self-end sm:w-28" />
                </div>
                <Skeleton className="h-3 w-80 max-w-full" />
                <div className="flex flex-wrap gap-2">
                  {Array.from({ length: 4 }).map((_, idx) => (
                    <Skeleton key={idx} className="h-8 w-16 rounded-xl" />
                  ))}
                </div>
              </div>
            </CardContent>
          </Card>
        ) : null}
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3">
          <div className="space-y-2">
            <Skeleton className="h-5 w-40" />
            <Skeleton className="h-4 w-80 max-w-full" />
          </div>
        </CardHeader>
        <CardContent className="p-0">
          <div className="grid grid-cols-4 gap-3 border-b border-border px-4 py-3 sm:px-6">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-4 w-20" />
            <Skeleton className="ml-auto h-4 w-24" />
            <Skeleton className="ml-auto h-4 w-24" />
          </div>
          <div className="space-y-3 p-4 sm:px-6">
            {Array.from({ length: 10 }).map((_, idx) => (
              <div key={idx} className="grid grid-cols-4 items-center gap-3">
                <Skeleton className="h-4 w-36" />
                <Skeleton className="h-5 w-20 rounded-full" />
                <Skeleton className="ml-auto h-4 w-24" />
                <Skeleton className="ml-auto h-4 w-24" />
              </div>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

export function BillingPageSkeleton({ topupEnabled = true }: BillingSkeletonProps) {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-2">
          <Skeleton className="h-7 w-44" />
          <Skeleton className="h-4 w-[560px] max-w-full" />
        </div>
      </div>
      <BillingContentSkeleton topupEnabled={topupEnabled} />
    </div>
  );
}


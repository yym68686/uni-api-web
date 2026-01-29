"use client";

import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function InviteContentSkeleton() {
  return (
    <div className="space-y-6">
      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-3">
          <div className="space-y-2">
            <Skeleton className="h-5 w-44" />
            <Skeleton className="h-4 w-[520px] max-w-full" />
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-2">
            <Skeleton className="h-10 w-full rounded-xl" />
          </div>
        </CardContent>
      </Card>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {Array.from({ length: 3 }).map((_, idx) => (
          <div
            key={idx}
            className={cn(
              "uai-cq uai-kpi-card relative overflow-hidden rounded-xl border border-border bg-card p-6"
            )}
          >
            <div className="flex items-center justify-between">
              <Skeleton className="h-4 w-28" />
              <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-border bg-background/40">
                <Skeleton className="h-4 w-4 rounded-md bg-muted/50" />
              </div>
            </div>
            <div className="mt-4 space-y-2">
              <Skeleton className="h-7 w-24" />
              <Skeleton className="h-3.5 w-40" />
            </div>
            <div className="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-primary/10 blur-2xl opacity-70" />
          </div>
        ))}
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3">
          <div className="space-y-2">
            <Skeleton className="h-5 w-48" />
            <Skeleton className="h-4 w-[520px] max-w-full" />
          </div>
        </CardHeader>
        <CardContent className="p-0">
          <div className="grid grid-cols-4 gap-3 border-b border-border px-4 py-3 sm:px-6">
            <Skeleton className="h-4 w-28" />
            <Skeleton className="h-4 w-20" />
            <Skeleton className="ml-auto h-4 w-24" />
            <Skeleton className="ml-auto h-4 w-24" />
          </div>
          <div className="space-y-3 p-4 sm:px-6">
            {Array.from({ length: 8 }).map((_, idx) => (
              <div key={idx} className="grid grid-cols-4 items-center gap-3">
                <Skeleton className="h-4 w-56" />
                <Skeleton className="h-5 w-20 rounded-full" />
                <Skeleton className="ml-auto h-4 w-20" />
                <Skeleton className="ml-auto h-4 w-28" />
              </div>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

export function InvitePageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-2">
          <Skeleton className="h-7 w-44" />
          <Skeleton className="h-4 w-[560px] max-w-full" />
        </div>
      </div>
      <InviteContentSkeleton />
    </div>
  );
}

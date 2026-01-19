"use client";

import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function BillingPageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-border bg-card p-6 lg:col-span-2">
          <div className="flex items-center justify-between">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-10 w-10 rounded-xl" />
          </div>
          <div className="mt-4 space-y-2">
            <Skeleton className="h-7 w-40" />
            <Skeleton className="h-4 w-24" />
          </div>
        </div>
      </div>

      <Card>
        <CardContent className="p-0">
          <div className="flex items-center gap-2 border-b border-border px-4 py-4 sm:px-6">
            <Skeleton className="h-4 w-32" />
          </div>
          <div className="grid grid-cols-4 gap-3 border-b border-border px-4 py-3 sm:px-6">
            {Array.from({ length: 4 }).map((_, idx) => (
              <Skeleton key={idx} className="h-4 w-20" />
            ))}
          </div>
          <div className="space-y-3 p-4 sm:px-6">
            {Array.from({ length: 10 }).map((_, idx) => (
              <div key={idx} className="grid grid-cols-4 items-center gap-3">
                {Array.from({ length: 4 }).map((__, jdx) => (
                  <Skeleton key={jdx} className="h-4 w-20" />
                ))}
              </div>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

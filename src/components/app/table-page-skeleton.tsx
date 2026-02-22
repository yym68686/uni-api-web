"use client";

import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";

interface SkeletonProps {
  className: string;
}

function Skeleton({ className }: SkeletonProps) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

interface TableContentSkeletonProps {
  columns?: number;
}

function normalizeColumns(value: number | undefined) {
  const raw = value ?? 8;
  const rounded = Math.round(raw);
  return Math.min(Math.max(rounded, 1), 12);
}

export function TableContentSkeleton({ columns }: TableContentSkeletonProps) {
  const safeColumns = normalizeColumns(columns);
  const gridStyle = { gridTemplateColumns: `repeat(${safeColumns}, minmax(0, 1fr))` };
  return (
    <Card>
      <CardContent className="p-0">
        <div className="flex items-center gap-2 border-b border-border px-4 py-4 sm:px-6">
          <Skeleton className="h-4 w-40" />
        </div>
        <div className="grid gap-3 border-b border-border px-4 py-3 sm:px-6" style={gridStyle}>
          {Array.from({ length: safeColumns }).map((_, idx) => (
            <Skeleton key={idx} className="h-4 w-16" />
          ))}
        </div>
        <div className="space-y-3 p-4 sm:px-6">
          {Array.from({ length: 10 }).map((_, idx) => (
            <div key={idx} className="grid items-center gap-3" style={gridStyle}>
              {Array.from({ length: safeColumns }).map((__, jdx) => (
                <Skeleton key={jdx} className="h-4 w-16" />
              ))}
            </div>
          ))}
        </div>
        <div className="flex justify-center border-t border-border py-4">
          <Skeleton className="h-9 w-32 rounded-xl" />
        </div>
      </CardContent>
    </Card>
  );
}

interface TablePageSkeletonProps {
  columns?: number;
}

export function TablePageSkeleton({ columns }: TablePageSkeletonProps) {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-2">
          <Skeleton className="h-7 w-24" />
          <Skeleton className="h-4 w-[560px] max-w-full" />
        </div>
        <Skeleton className="h-10 w-40 rounded-xl" />
      </div>
      <TableContentSkeleton columns={columns} />
    </div>
  );
}

import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function DashboardKpisSkeleton() {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      {Array.from({ length: 4 }).map((_, idx) => (
        <div
          key={idx}
          className={cn(
            "uai-cq uai-kpi-card relative overflow-hidden rounded-xl border border-border bg-card p-6"
          )}
        >
          <div className="uai-kpi-head flex items-center justify-between">
            <Skeleton className="h-4 w-28" />
            <div className="uai-kpi-icon flex h-10 w-10 items-center justify-center rounded-xl border border-border bg-background/40">
              <Skeleton className="h-4 w-4 rounded-md bg-muted/50" />
            </div>
          </div>
          <div className="mt-4 space-y-2">
            <Skeleton className="h-7 w-24" />
            <Skeleton className="h-3.5 w-20" />
          </div>
        </div>
      ))}
    </div>
  );
}

export function DashboardChartSkeleton() {
  return (
    <Card className="lg:col-span-2">
      <CardHeader>
        <Skeleton className="h-5 w-32" />
        <Skeleton className="h-4 w-56" />
      </CardHeader>
      <CardContent>
        <div className="h-72 w-full rounded-xl border border-border bg-muted/10 animate-pulse" />
      </CardContent>
    </Card>
  );
}

export function DashboardAnnouncementsSkeleton() {
  return (
    <Card className="bg-warning/10">
      <CardHeader>
        <Skeleton className="h-5 w-28" />
        <Skeleton className="h-4 w-44" />
      </CardHeader>
      <CardContent className="space-y-3">
        {Array.from({ length: 3 }).map((_, idx) => (
          <div
            key={idx}
            className="rounded-xl border border-border bg-background/35 p-3"
          >
            <Skeleton className="h-4 w-4/5" />
            <div className="mt-2 flex items-center gap-2">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="h-3 w-20" />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

export function DashboardSkeleton() {
  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <Skeleton className="h-7 w-64" />
        <Skeleton className="h-4 w-80" />
      </div>
      <DashboardKpisSkeleton />
      <div className="grid gap-4 lg:grid-cols-3">
        <DashboardChartSkeleton />
        <DashboardAnnouncementsSkeleton />
      </div>
    </div>
  );
}


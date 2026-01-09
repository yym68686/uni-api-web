import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

interface AdminAnnouncementsCardSkeletonProps {
  showActions?: boolean;
}

export function AdminAnnouncementsCardSkeleton({ showActions = true }: AdminAnnouncementsCardSkeletonProps) {
  const cols = showActions ? 5 : 4;

  return (
    <Card>
      <CardHeader>
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-4 w-[520px] max-w-full" />
      </CardHeader>
      <CardContent>
        <div className="rounded-xl border border-border bg-muted/10">
          <div className={cn("grid gap-3 border-b border-border px-4 py-3", cols === 5 ? "grid-cols-5" : "grid-cols-4")}>
            {Array.from({ length: cols }).map((_, idx) => (
              <Skeleton key={idx} className="h-4 w-24" />
            ))}
          </div>
          <div className="space-y-3 p-4">
            {Array.from({ length: 6 }).map((_, idx) => (
              <div
                key={idx}
                className={cn("grid items-center gap-3", cols === 5 ? "grid-cols-5" : "grid-cols-4")}
              >
                {Array.from({ length: cols }).map((__, jdx) => (
                  <Skeleton key={jdx} className="h-4 w-24" />
                ))}
              </div>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

export function AdminAnnouncementsPageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-2">
          <Skeleton className="h-7 w-56" />
          <Skeleton className="h-4 w-[640px] max-w-full" />
        </div>
        <Skeleton className="h-10 w-44 rounded-xl" />
      </div>
      <AdminAnnouncementsCardSkeleton />
    </div>
  );
}


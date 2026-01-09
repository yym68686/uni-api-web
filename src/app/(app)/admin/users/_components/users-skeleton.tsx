import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function AdminUsersCardSkeleton() {
  return (
    <Card>
      <CardHeader>
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-4 w-[640px] max-w-full" />
      </CardHeader>
      <CardContent>
        <div className="rounded-xl border border-border bg-muted/10">
          <div className="grid grid-cols-10 gap-3 border-b border-border px-4 py-3">
            {Array.from({ length: 10 }).map((_, idx) => (
              <Skeleton key={idx} className="h-4 w-20" />
            ))}
          </div>
          <div className="space-y-3 p-4">
            {Array.from({ length: 10 }).map((_, idx) => (
              <div key={idx} className="grid grid-cols-10 items-center gap-3">
                {Array.from({ length: 10 }).map((__, jdx) => (
                  <Skeleton key={jdx} className="h-4 w-20" />
                ))}
              </div>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

export function AdminUsersPageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-2">
          <Skeleton className="h-7 w-40" />
          <Skeleton className="h-4 w-[680px] max-w-full" />
        </div>
      </div>
      <AdminUsersCardSkeleton />
    </div>
  );
}


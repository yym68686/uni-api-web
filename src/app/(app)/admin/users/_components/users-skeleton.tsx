import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function AdminUsersCardSkeleton() {
  return (
    <Card>
      <CardContent className="p-0">
        <div className="border-b border-border px-4 py-4 sm:px-6">
          <div className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
            <div className="w-full max-w-2xl space-y-2">
              <Skeleton className="h-3 w-28" />
              <Skeleton className="h-11 w-full rounded-2xl" />
            </div>
            <div className="flex gap-2">
              <Skeleton className="h-11 w-24 rounded-2xl" />
              <Skeleton className="h-11 w-24 rounded-2xl" />
            </div>
          </div>
        </div>
        <div className="grid grid-cols-10 gap-3 border-b border-border px-4 py-3 sm:px-6">
          {Array.from({ length: 10 }).map((_, idx) => (
            <Skeleton key={idx} className="h-4 w-20" />
          ))}
        </div>
        <div className="space-y-3 p-4 sm:px-6">
          {Array.from({ length: 10 }).map((_, idx) => (
            <div key={idx} className="grid grid-cols-10 items-center gap-3">
              {Array.from({ length: 10 }).map((__, jdx) => (
                <Skeleton key={jdx} className="h-4 w-20" />
              ))}
            </div>
          ))}
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

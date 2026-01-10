import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function ModelsContentSkeleton() {
  return (
    <Card>
      <CardContent className="p-0">
        <div className="grid grid-cols-3 gap-3 border-b border-border px-4 py-3 sm:px-6">
          <Skeleton className="h-4 w-28" />
          <Skeleton className="h-4 w-24" />
          <Skeleton className="h-4 w-24" />
        </div>
        <div className="space-y-3 p-4 sm:px-6">
          {Array.from({ length: 8 }).map((_, idx) => (
            <div key={idx} className="grid grid-cols-3 items-center gap-3">
              <Skeleton className="h-4 w-56" />
              <Skeleton className="h-4 w-20" />
              <Skeleton className="h-4 w-20" />
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

export function ModelsPageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-2">
          <Skeleton className="h-7 w-28" />
          <Skeleton className="h-4 w-[520px] max-w-full" />
          <Skeleton className="h-3 w-80 max-w-full" />
        </div>
      </div>
      <ModelsContentSkeleton />
    </div>
  );
}

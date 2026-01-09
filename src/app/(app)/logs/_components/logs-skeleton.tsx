import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function LogsPageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <Skeleton className="h-7 w-24" />
        <Skeleton className="h-4 w-[560px] max-w-full" />
      </div>

      <Card>
        <CardHeader className="pb-4">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-4 w-32" />
        </CardHeader>
        <CardContent>
          <div className="rounded-xl border border-border bg-muted/10">
            <div className="grid grid-cols-9 gap-3 border-b border-border px-4 py-3">
              {Array.from({ length: 9 }).map((_, idx) => (
                <Skeleton key={idx} className="h-4 w-16" />
              ))}
            </div>
            <div className="space-y-3 p-4">
              {Array.from({ length: 10 }).map((_, idx) => (
                <div key={idx} className="grid grid-cols-9 items-center gap-3">
                  {Array.from({ length: 9 }).map((__, jdx) => (
                    <Skeleton key={jdx} className="h-4 w-16" />
                  ))}
                </div>
              ))}
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}


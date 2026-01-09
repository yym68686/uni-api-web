import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function ModelsPageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <Skeleton className="h-7 w-28" />
        <Skeleton className="h-4 w-[520px] max-w-full" />
      </div>

      <Card>
        <CardHeader className="pb-4">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-4 w-[480px] max-w-full" />
        </CardHeader>
        <CardContent>
          <div className="rounded-xl border border-border bg-muted/10">
            <div className="grid grid-cols-3 gap-3 border-b border-border px-4 py-3">
              <Skeleton className="h-4 w-28" />
              <Skeleton className="h-4 w-24" />
              <Skeleton className="h-4 w-24" />
            </div>
            <div className="space-y-3 p-4">
              {Array.from({ length: 8 }).map((_, idx) => (
                <div key={idx} className="grid grid-cols-3 items-center gap-3">
                  <Skeleton className="h-4 w-56" />
                  <Skeleton className="h-4 w-20" />
                  <Skeleton className="h-4 w-20" />
                </div>
              ))}
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}


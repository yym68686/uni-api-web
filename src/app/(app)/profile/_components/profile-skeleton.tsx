import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function ProfilePageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <Skeleton className="h-7 w-28" />
        <Skeleton className="h-4 w-[520px] max-w-full" />
      </div>

      <Card>
        <CardHeader>
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-4 w-64" />
        </CardHeader>
        <CardContent>
          <div className="grid gap-4 sm:grid-cols-2">
            {Array.from({ length: 7 }).map((_, idx) => (
              <div
                key={idx}
                className={cn(
                  "rounded-xl border border-border bg-background/35 p-4",
                  idx === 6 ? "sm:col-span-2" : null
                )}
              >
                <Skeleton className="h-3 w-24" />
                <Skeleton className="mt-2 h-4 w-40" />
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      <Card className="border-destructive/40 bg-destructive/5">
        <CardHeader>
          <Skeleton className="h-5 w-36" />
          <Skeleton className="h-4 w-80 max-w-full" />
        </CardHeader>
        <CardContent className="flex items-center justify-between gap-4">
          <Skeleton className="h-4 w-72 max-w-full" />
          <Skeleton className="h-10 w-28 rounded-xl" />
        </CardContent>
      </Card>
    </div>
  );
}


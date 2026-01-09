import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function AdminChannelsCardSkeleton() {
  return (
    <Card>
      <CardHeader>
        <Skeleton className="h-5 w-36" />
        <Skeleton className="h-4 w-[520px] max-w-full" />
      </CardHeader>
      <CardContent>
        <div className="rounded-xl border border-border bg-muted/10">
          <div className="grid grid-cols-6 gap-3 border-b border-border px-4 py-3">
            <Skeleton className="h-4 w-28" />
            <Skeleton className="h-4 w-32" />
            <Skeleton className="h-4 w-28" />
            <Skeleton className="h-4 w-28" />
            <Skeleton className="h-4 w-28" />
            <Skeleton className="ml-auto h-4 w-10" />
          </div>
          <div className="space-y-3 p-4">
            {Array.from({ length: 7 }).map((_, idx) => (
              <div key={idx} className="grid grid-cols-6 items-center gap-3">
                <Skeleton className="h-4 w-28" />
                <Skeleton className="h-4 w-40" />
                <Skeleton className="h-4 w-36" />
                <Skeleton className="h-4 w-32" />
                <Skeleton className="h-4 w-24" />
                <Skeleton className="ml-auto h-8 w-8 rounded-xl" />
              </div>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

export function AdminChannelsPageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="space-y-2">
          <Skeleton className="h-7 w-52" />
          <Skeleton className="h-4 w-[560px] max-w-full" />
        </div>
        <Skeleton className="h-10 w-44 rounded-xl" />
      </div>
      <AdminChannelsCardSkeleton />
    </div>
  );
}


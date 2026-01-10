import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

function Skeleton({ className }: { className: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function AdminSettingsCardSkeleton() {
  return (
    <Card>
      <CardHeader>
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-4 w-[520px] max-w-full" />
      </CardHeader>
      <CardContent>
        <div className="rounded-xl border border-border bg-muted/10 p-4">
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-2">
              <Skeleton className="h-4 w-52" />
              <Skeleton className="h-4 w-[420px] max-w-full" />
            </div>
            <Skeleton className="h-6 w-11 rounded-full" />
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

export function AdminSettingsPageSkeleton() {
  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <Skeleton className="h-7 w-44" />
        <Skeleton className="h-4 w-[560px] max-w-full" />
      </div>
      <AdminSettingsCardSkeleton />
    </div>
  );
}


function Skeleton({ className }: { className: string }) {
  return <div className={`animate-pulse rounded-xl bg-muted/30 ${className}`} />;
}

export function AdminAnalyticsSkeleton() {
  return (
    <div className="space-y-6">
      <div className="rounded-2xl border border-border bg-background/70 p-4 backdrop-blur-xl">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex flex-wrap items-center gap-2">
            <Skeleton className="h-9 w-40" />
            <Skeleton className="h-9 w-28" />
            <Skeleton className="h-9 w-28" />
          </div>
          <Skeleton className="h-9 w-56" />
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Skeleton className="h-[132px]" />
        <Skeleton className="h-[132px]" />
        <Skeleton className="h-[132px]" />
        <Skeleton className="h-[132px]" />
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4">
        <Skeleton className="h-[132px]" />
        <Skeleton className="h-[132px]" />
        <Skeleton className="h-[132px]" />
        <Skeleton className="h-[132px]" />
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="rounded-2xl border border-border bg-card p-6">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="mt-4 h-64 w-full" />
        </div>
        <div className="rounded-2xl border border-border bg-card p-6">
          <Skeleton className="h-5 w-28" />
          <Skeleton className="mt-4 h-64 w-full" />
        </div>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <div className="rounded-2xl border border-border bg-card p-6">
          <Skeleton className="h-5 w-28" />
          <div className="mt-4 space-y-3">
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-5/6" />
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-4 w-4/5" />
          </div>
        </div>
        <div className="rounded-2xl border border-border bg-card p-6">
          <Skeleton className="h-5 w-28" />
          <div className="mt-4 space-y-3">
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-5/6" />
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-4 w-4/5" />
          </div>
        </div>
        <div className="rounded-2xl border border-border bg-card p-6">
          <Skeleton className="h-5 w-28" />
          <div className="mt-4 space-y-3">
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-5/6" />
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-4 w-4/5" />
          </div>
        </div>
      </div>
    </div>
  );
}


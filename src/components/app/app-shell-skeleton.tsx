import { cn } from "@/lib/utils";

interface SkeletonProps {
  className: string;
}

function Skeleton({ className }: SkeletonProps) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

export function AppShellSkeleton() {
  return (
    <div className="min-h-dvh bg-background">
      <aside className="fixed inset-y-0 left-0 hidden w-64 border-r border-border bg-sidebar/40 p-4 sm:block">
        <div className="space-y-4">
          <Skeleton className="h-9 w-36 rounded-xl" />
          <div className="space-y-2">
            {Array.from({ length: 6 }).map((_, idx) => (
              <Skeleton key={idx} className="h-9 w-full rounded-xl" />
            ))}
          </div>
        </div>
      </aside>

      <div className="flex min-h-dvh min-w-0 flex-col sm:pl-64">
        <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur-md">
          <div className="mx-auto flex h-14 max-w-6xl items-center justify-between px-4 sm:px-6">
            <Skeleton className="h-9 w-10 rounded-xl sm:hidden" />
            <Skeleton className="h-6 w-40 rounded-xl" />
            <Skeleton className="h-9 w-24 rounded-xl" />
          </div>
        </header>

        <main id="main" className="min-w-0 flex-1 p-4 sm:p-6">
          <div className="mx-auto w-full max-w-6xl space-y-6">
            <div className="space-y-2">
              <Skeleton className="h-8 w-52" />
              <Skeleton className="h-4 w-[520px] max-w-full" />
            </div>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {Array.from({ length: 3 }).map((_, idx) => (
                <Skeleton key={idx} className="h-32 rounded-2xl" />
              ))}
            </div>
            <Skeleton className="h-72 rounded-2xl" />
          </div>
        </main>
      </div>
    </div>
  );
}


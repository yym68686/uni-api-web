import { cn } from "@/lib/utils";
import { getAppName } from "@/lib/app-config";
import { BrandPanel } from "@/components/auth/brand-panel";

interface SkeletonProps {
  className: string;
}

function Skeleton({ className }: SkeletonProps) {
  return <div className={cn("animate-pulse rounded-lg bg-muted/40", className)} />;
}

interface AuthPageSkeletonProps {
  variant: "login" | "register";
}

export async function AuthPageSkeleton({ variant }: AuthPageSkeletonProps) {
  const appName = getAppName();
  const showConfirmPassword = variant === "register";
  const showVerificationCode = variant === "register";

  return (
    <div className="min-h-dvh bg-background">
      <div className="grid min-h-dvh grid-cols-1 lg:grid-cols-2">
        <BrandPanel appName={appName} />
        <section id="main" className="flex items-center justify-center px-6 py-12 lg:px-12">
          <div className="w-full max-w-md space-y-6">
            <div className="space-y-2">
              <Skeleton className="h-7 w-48" />
              <Skeleton className="h-4 w-72 max-w-full" />
            </div>

            <div className="space-y-3">
              <Skeleton className="h-10 w-full rounded-xl" />
              <Skeleton className="h-10 w-full rounded-xl" />
              {showConfirmPassword ? <Skeleton className="h-10 w-full rounded-xl" /> : null}
              {showVerificationCode ? <Skeleton className="h-10 w-full rounded-xl" /> : null}
              <Skeleton className="h-10 w-full rounded-xl" />
              <Skeleton className="h-4 w-40" />
            </div>

            <div className="space-y-2">
              <Skeleton className="h-9 w-full rounded-xl" />
              <Skeleton className="h-9 w-full rounded-xl" />
            </div>

            <Skeleton className="h-4 w-56" />
          </div>
        </section>
      </div>
    </div>
  );
}


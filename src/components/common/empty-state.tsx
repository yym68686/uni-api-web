import type * as React from "react";

import { cn } from "@/lib/utils";

interface EmptyStateProps {
  icon: React.ReactNode;
  title: React.ReactNode;
  description?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}

export function EmptyState({ icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div className={cn("rounded-xl border border-dashed border-border bg-muted/10 p-10 text-center", className)}>
      <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl border border-border bg-background/50">
        {icon}
      </div>
      <div className="mt-4 text-base font-semibold text-foreground">{title}</div>
      {description ? <div className="mt-1 text-sm text-muted-foreground">{description}</div> : null}
      {action ? <div className="mt-5 flex justify-center">{action}</div> : null}
    </div>
  );
}


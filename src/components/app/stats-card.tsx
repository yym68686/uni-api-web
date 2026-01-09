import type { LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";

interface StatsCardProps {
  title: string;
  value: string;
  trend?: string;
  icon: LucideIcon;
  iconGradientClassName?: string;
  className?: string;
}

export function StatsCard({
  title,
  value,
  trend,
  icon: Icon,
  iconGradientClassName,
  className
}: StatsCardProps) {
  return (
    <div
      className={cn(
        "group relative overflow-hidden rounded-xl border border-border bg-card p-6",
        "transition-all duration-300 hover:scale-[1.02] hover:shadow-lg hover:border-primary/20",
        className
      )}
    >
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-muted-foreground">{title}</h3>
        <div
          className={cn(
            "flex h-10 w-10 items-center justify-center rounded-xl",
            "text-foreground ring-1 ring-inset ring-border/60",
            "bg-gradient-to-br from-primary/30 to-primary/10",
            "transition-all duration-300 group-hover:scale-[1.03]",
            iconGradientClassName
          )}
        >
          <Icon className="h-4 w-4" />
        </div>
      </div>
      <div className="mt-4">
        <div className="text-2xl font-bold tracking-tight text-foreground">
          <span className="tabular-nums">{value}</span>
        </div>
        {trend ? (
          <p className="mt-1 text-xs text-success font-mono">{trend}</p>
        ) : (
          <p className="mt-1 text-xs text-muted-foreground font-mono">
            last 24h
          </p>
        )}
      </div>

      <div className="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-primary/10 blur-2xl transition-opacity duration-300 group-hover:opacity-100 opacity-70" />
    </div>
  );
}

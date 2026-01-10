"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

interface ClientDateTimeProps {
  value?: string | null;
  locale?: string;
  fallback?: string;
  dateStyle?: Intl.DateTimeFormatOptions["dateStyle"];
  timeStyle?: Intl.DateTimeFormatOptions["timeStyle"];
  className?: string;
}

export function ClientDateTime({
  value,
  locale,
  fallback = "—",
  dateStyle = "medium",
  timeStyle = "short",
  className
}: ClientDateTimeProps) {
  const [mounted, setMounted] = React.useState(false);

  React.useEffect(() => {
    setMounted(true);
  }, []);

  if (!mounted) {
    return (
      <span className={cn("font-mono tabular-nums", className)} suppressHydrationWarning>
        {fallback}
      </span>
    );
  }

  if (!value) {
    return <span className={cn("font-mono tabular-nums", className)}>{fallback}</span>;
  }

  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) {
    return <span className={cn("font-mono tabular-nums", className)}>{fallback}</span>;
  }

  const formatted = new Intl.DateTimeFormat(locale, { dateStyle, timeStyle }).format(dt);

  return (
    <span className={cn("font-mono tabular-nums", className)} title={dt.toISOString()}>
      {formatted}
    </span>
  );
}


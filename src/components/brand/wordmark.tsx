"use client";

import { cn } from "@/lib/utils";

interface BrandWordmarkProps {
  name: string;
  className?: string;
}

export function BrandWordmark({ name, className }: BrandWordmarkProps) {
  return (
    <span className={cn("font-logo tracking-tight text-foreground", className)}>
      {name}
    </span>
  );
}


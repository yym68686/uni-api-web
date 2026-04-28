import type { HTMLAttributes, ReactNode } from "react";

import { cn } from "@/lib/utils";

export interface IconSwapProps extends HTMLAttributes<HTMLSpanElement> {
  state: "a" | "b";
  iconA: ReactNode;
  iconB: ReactNode;
  itemClassName?: string;
  decorative?: boolean;
}

export function IconSwap({
  state,
  iconA,
  iconB,
  className,
  itemClassName,
  decorative = true,
  ...props
}: IconSwapProps) {
  return (
    <span
      className={cn("uai-icon-swap", className)}
      data-state={state}
      aria-hidden={decorative}
      {...props}
    >
      <span className={cn("uai-icon-swap-item", itemClassName)} data-icon="a">
        {iconA}
      </span>
      <span className={cn("uai-icon-swap-item", itemClassName)} data-icon="b">
        {iconB}
      </span>
    </span>
  );
}

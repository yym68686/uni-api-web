import type * as React from "react";

import { cn } from "@/lib/utils";
import { SpotlightOverlay } from "@/components/landing/spotlight-overlay";

interface SpotlightSurfaceProps {
  children: React.ReactNode;
  className?: string;
}

function createId() {
  const uuid =
    typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Math.random()}`;
  return `uai-spot-${uuid.replaceAll("-", "")}`;
}

export function SpotlightSurface({ children, className }: SpotlightSurfaceProps) {
  const id = createId();

  return (
    <div
      id={id}
      className={cn(
        "group relative overflow-hidden rounded-2xl border border-border",
        "bg-card/40 backdrop-blur-xl",
        "transition-all duration-300 [transition-timing-function:cubic-bezier(0.16,1,0.3,1)]",
        "hover:-translate-y-1 hover:shadow-lg hover:border-primary/20",
        className
      )}
      style={
        {
          ["--uai-spot-x" as string]: "50%",
          ["--uai-spot-y" as string]: "50%"
        } as React.CSSProperties
      }
    >
      <SpotlightOverlay targetId={id} />
      <div
        aria-hidden="true"
        className={cn(
          "pointer-events-none absolute inset-0 opacity-0 transition-opacity duration-300",
          "group-hover:opacity-100"
        )}
        style={{
          backgroundImage:
            "radial-gradient(320px circle at var(--uai-spot-x) var(--uai-spot-y), oklch(var(--primary) / 0.18), transparent 62%)"
        }}
      />
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-foreground/10 to-transparent"
      />
      <div className="relative">{children}</div>
    </div>
  );
}


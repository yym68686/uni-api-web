"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

interface SwitchProps
  extends Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "onChange"> {
  checked?: boolean;
  defaultChecked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
}

export function Switch({
  checked,
  defaultChecked,
  onCheckedChange,
  className,
  disabled,
  onClick,
  ...props
}: SwitchProps) {
  const [uncontrolled, setUncontrolled] = React.useState(Boolean(defaultChecked));
  const isControlled = typeof checked === "boolean";
  const value = isControlled ? Boolean(checked) : uncontrolled;

  return (
    <button
      type="button"
      role="switch"
      aria-checked={value}
      disabled={disabled}
      onClick={(e) => {
        const next = !value;
        if (!isControlled) setUncontrolled(next);
        onCheckedChange?.(next);
        onClick?.(e);
      }}
      className={cn(
        "relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border p-0.5",
        "border-border bg-muted/40 transition-colors duration-200",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background",
        "disabled:cursor-not-allowed disabled:opacity-50",
        value ? "border-primary/40 bg-primary/25" : "hover:bg-muted/55",
        className
      )}
      {...props}
    >
      <span
        aria-hidden="true"
        className={cn(
          "pointer-events-none block h-5 w-5 rounded-full bg-background shadow-sm",
          "transition-transform duration-200",
          value ? "translate-x-5" : "translate-x-0"
        )}
      />
    </button>
  );
}


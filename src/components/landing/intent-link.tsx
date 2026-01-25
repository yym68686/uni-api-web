"use client";

import Link from "next/link";
import type { VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";
import { buttonVariants } from "@/components/ui/button";

interface IntentLinkProps extends VariantProps<typeof buttonVariants> {
  href: string;
  children: React.ReactNode;
  className?: string;
  ariaLabel?: string;
}

export function IntentLink({ href, children, className, variant, size, ariaLabel }: IntentLinkProps) {
  return (
    <Link
      href={href}
      prefetch
      aria-label={ariaLabel}
      className={cn(buttonVariants({ variant, size, className }))}
    >
      {children}
    </Link>
  );
}

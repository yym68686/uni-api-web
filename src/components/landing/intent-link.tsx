"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
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
  const router = useRouter();

  const prefetch = React.useCallback(() => {
    router.prefetch(href);
  }, [router, href]);

  return (
    <Link
      href={href}
      prefetch={false}
      aria-label={ariaLabel}
      onMouseEnter={prefetch}
      onFocus={prefetch}
      onTouchStart={prefetch}
      className={cn(buttonVariants({ variant, size, className }))}
    >
      {children}
    </Link>
  );
}


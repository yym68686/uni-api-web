"use client";

import * as React from "react";
import { usePathname } from "next/navigation";

import { clearPendingPathname } from "@/lib/navigation-intent";

export function NavigationIntentCleaner() {
  const pathname = usePathname();

  React.useEffect(() => {
    clearPendingPathname();
  }, [pathname]);

  return null;
}


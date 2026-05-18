"use client";

import * as React from "react";
import { usePathname } from "next/navigation";

import { trackDataOceanPageView } from "@/lib/dataocean";

export function DataOceanTracker() {
  const pathname = usePathname();
  const lastPathRef = React.useRef<string | null>(null);

  React.useEffect(() => {
    const path = `${pathname ?? ""}${window.location.search}`;
    if (!pathname || lastPathRef.current === path) return;
    lastPathRef.current = path;

    trackDataOceanPageView("page_view");
    if (pathname === "/") {
      trackDataOceanPageView("landing_view");
    }
  }, [pathname]);

  return null;
}

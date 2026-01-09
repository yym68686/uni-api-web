"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ChevronRight } from "lucide-react";

import { cn } from "@/lib/utils";

const labelMap: Record<string, string> = {
  "/": "Home",
  "/dashboard": "Dashboard",
  "/keys": "API Keys",
  "/models": "Models",
  "/logs": "Logs",
  "/profile": "Profile",
};

interface BreadcrumbItem {
  href: string;
  label: string;
}

function buildCrumbs(pathname: string): BreadcrumbItem[] {
  const homeHref = "/";
  const homeLabel = labelMap[homeHref] ?? "Home";
  const rootHref = "/dashboard";
  const rootLabel = labelMap[rootHref] ?? "Dashboard";

  if (pathname === rootHref) {
    return [
      { href: homeHref, label: homeLabel },
      { href: rootHref, label: rootLabel }
    ];
  }

  const segments = pathname.split("/").filter(Boolean);
  const crumbs: BreadcrumbItem[] = [
    { href: homeHref, label: homeLabel },
    { href: rootHref, label: rootLabel }
  ];

  let current = "";
  for (const seg of segments) {
    current += `/${seg}`;
    if (current === rootHref) continue;
    const label = labelMap[current] ?? seg;
    crumbs.push({ href: current, label });
  }
  return crumbs;
}

interface BreadcrumbsProps {
  className?: string;
}

export function Breadcrumbs({ className }: BreadcrumbsProps) {
  const pathname = usePathname();
  const crumbs = React.useMemo(() => buildCrumbs(pathname), [pathname]);

  return (
    <nav className={cn("flex items-center gap-1.5 text-sm", className)} aria-label="Breadcrumb">
      {crumbs.map((c, idx) => {
        const last = idx === crumbs.length - 1;
        return (
          <React.Fragment key={c.href}>
            {idx > 0 ? (
              <ChevronRight className="h-4 w-4 text-muted-foreground" />
            ) : null}
            {last ? (
              <span className="font-medium text-foreground">{c.label}</span>
            ) : (
              <Link
                href={c.href}
                className="text-muted-foreground hover:text-foreground transition-colors"
              >
                {c.label}
              </Link>
            )}
          </React.Fragment>
        );
      })}
    </nav>
  );
}

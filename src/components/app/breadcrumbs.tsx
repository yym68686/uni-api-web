"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ChevronRight } from "lucide-react";

import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";
import type { MessageKey } from "@/lib/i18n/messages";

const labelKeyMap: Record<string, MessageKey> = {
  "/": "app.home",
  "/dashboard": "app.dashboard",
  "/keys": "app.keys",
  "/models": "app.models",
  "/logs": "app.logs",
  "/billing": "app.billing",
  "/profile": "app.profile",
  "/admin": "app.admin",
  "/admin/channels": "app.admin.channels",
  "/admin/models": "app.admin.modelConfig",
  "/admin/users": "app.admin.users",
  "/admin/announcements": "app.admin.announcements",
  "/admin/settings": "app.admin.settings"
};

type BreadcrumbItem = { href: string; labelKey: MessageKey } | { href: string; label: string };

function buildCrumbs(pathname: string): BreadcrumbItem[] {
  const homeHref = "/";
  const homeLabelKey = labelKeyMap[homeHref] ?? "app.home";
  const rootHref = "/dashboard";
  const rootLabelKey = labelKeyMap[rootHref] ?? "app.dashboard";

  if (pathname === rootHref) {
    return [
      { href: homeHref, labelKey: homeLabelKey },
      { href: rootHref, labelKey: rootLabelKey }
    ];
  }

  const segments = pathname.split("/").filter(Boolean);
  const crumbs: BreadcrumbItem[] = [
    { href: homeHref, labelKey: homeLabelKey },
    { href: rootHref, labelKey: rootLabelKey }
  ];

  let current = "";
  for (const seg of segments) {
    current += `/${seg}`;
    if (current === rootHref) continue;
    const labelKey = labelKeyMap[current];
    if (labelKey) {
      crumbs.push({ href: current, labelKey });
    } else {
      crumbs.push({ href: current, label: seg });
    }
  }
  return crumbs;
}

interface BreadcrumbsProps {
  className?: string;
}

export function Breadcrumbs({ className }: BreadcrumbsProps) {
  const pathname = usePathname();
  const { t } = useI18n();
  const crumbs = React.useMemo(() => buildCrumbs(pathname), [pathname]);

  return (
    <nav className={cn("flex items-center gap-1.5 text-sm", className)} aria-label="Breadcrumb">
      {crumbs.map((c, idx) => {
        const last = idx === crumbs.length - 1;
        const label = "labelKey" in c ? t(c.labelKey) : c.label;
        return (
          <React.Fragment key={c.href}>
            {idx > 0 ? (
              <ChevronRight className="h-4 w-4 text-muted-foreground" />
            ) : null}
            {last ? (
              <span className="font-medium text-foreground">{label}</span>
            ) : (
              <Link
                href={c.href}
                className="text-muted-foreground hover:text-foreground transition-colors"
              >
                {label}
              </Link>
            )}
          </React.Fragment>
        );
      })}
    </nav>
  );
}

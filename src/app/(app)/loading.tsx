"use client";

import { usePathname } from "next/navigation";

import { peekPendingPathname } from "@/lib/navigation-intent";
import { BillingPageSkeleton } from "./billing/_components/billing-skeleton";
import { InvitePageSkeleton } from "./invite/_components/invite-skeleton";
import { KeysPageSkeleton } from "./keys/_components/keys-skeleton";
import { LogsPageSkeleton } from "./logs/_components/logs-skeleton";
import { ModelsPageSkeleton } from "./models/_components/models-skeleton";
import { TablePageSkeleton } from "@/components/app/table-page-skeleton";

function normalizePathname(value: string) {
  const withoutHash = value.split("#")[0] ?? value;
  return (withoutHash.split("?")[0] ?? withoutHash).trim();
}

export default function Loading() {
  const pathname = usePathname();
  const pending = peekPendingPathname();
  const locationPathname = typeof window !== "undefined" ? window.location.pathname : null;
  const activePathname = normalizePathname(locationPathname ?? pending ?? pathname);
  const content =
    activePathname === "/keys" || activePathname.startsWith("/keys/")
      ? (
        <KeysPageSkeleton />
      )
      : activePathname === "/models" || activePathname.startsWith("/models/")
        ? (
          <ModelsPageSkeleton />
        )
        : activePathname === "/billing" || activePathname.startsWith("/billing/")
          ? (
            <BillingPageSkeleton />
          )
          : activePathname === "/invite" || activePathname.startsWith("/invite/")
            ? (
              <InvitePageSkeleton />
            )
          : activePathname === "/logs" || activePathname.startsWith("/logs/")
            ? (
              <LogsPageSkeleton />
            )
          : null;

  return content ?? <TablePageSkeleton />;
}

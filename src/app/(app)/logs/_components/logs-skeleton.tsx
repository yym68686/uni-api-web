"use client";

import { TableContentSkeleton, TablePageSkeleton } from "@/components/app/table-page-skeleton";

export function LogsContentSkeleton() {
  return <TableContentSkeleton columns={11} />;
}

export function LogsPageSkeleton() {
  return <TablePageSkeleton columns={11} />;
}

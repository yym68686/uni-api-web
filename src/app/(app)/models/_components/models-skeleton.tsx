"use client";

import { TableContentSkeleton, TablePageSkeleton } from "@/components/app/table-page-skeleton";

export function ModelsContentSkeleton() {
  return <TableContentSkeleton columns={3} />;
}

export function ModelsPageSkeleton() {
  return <TablePageSkeleton columns={3} />;
}

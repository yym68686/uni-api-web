import { AdminModelsTableClient } from "./models-table-client";
import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import type { AdminModelsListResponse } from "@/lib/types";

function isAdminModelsListResponse(value: unknown): value is AdminModelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

async function getModels() {
  const res = await fetch(buildBackendUrl("/admin/models"), {
    cache: "force-cache",
    next: { tags: ["models:admin-config", "models:user"] },
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isAdminModelsListResponse(json)) return null;
  return json.items;
}

interface AdminModelsContentProps {
  locale: Locale;
}

export async function AdminModelsContent({ locale }: AdminModelsContentProps) {
  const items = (await getModels()) ?? [];

  void locale;
  return <AdminModelsTableClient initialItems={items} />;
}

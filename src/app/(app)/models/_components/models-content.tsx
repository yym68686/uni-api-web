import { buildBackendUrl, getBackendAuthHeadersCached } from "@/lib/backend";
import type { Locale } from "@/lib/i18n/messages";
import type { ModelsListResponse } from "@/lib/types";
import { ModelsContentClient } from "./models-content-client";

function isModelsListResponse(value: unknown): value is ModelsListResponse {
  if (!value || typeof value !== "object") return false;
  if (!("items" in value)) return false;
  const items = (value as { items?: unknown }).items;
  return Array.isArray(items);
}

export async function getModels() {
  const res = await fetch(buildBackendUrl("/console/models"), {
    cache: "no-store",
    headers: await getBackendAuthHeadersCached()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isModelsListResponse(json)) return null;
  return json.items;
}

interface ModelsContentProps {
  locale: Locale;
  initialItems: ModelsListResponse["items"];
}

export function ModelsContent({ locale, initialItems }: ModelsContentProps) {
  return <ModelsContentClient locale={locale} initialItems={initialItems} />;
}

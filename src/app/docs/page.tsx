import { redirect } from "next/navigation";

import { getRequestLocale } from "@/lib/i18n/server";

export default async function DocsPage() {
  const locale = await getRequestLocale();
  redirect(`/docs/${locale}`);
}


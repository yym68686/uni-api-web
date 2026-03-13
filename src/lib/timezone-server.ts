import "server-only";

import { cookies } from "next/headers";
import { cache } from "react";

import { DEFAULT_TIME_ZONE, normalizeTimeZone, TIMEZONE_COOKIE_NAME } from "@/lib/timezone";

export const getRequestTimeZone = cache(async (): Promise<string> => {
  const cookieStore = await cookies();
  return normalizeTimeZone(cookieStore.get(TIMEZONE_COOKIE_NAME)?.value) ?? DEFAULT_TIME_ZONE;
});

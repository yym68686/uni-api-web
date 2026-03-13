"use client";

import * as React from "react";

import { getBrowserTimeZone, TIMEZONE_COOKIE_NAME } from "@/lib/timezone";

function readCookie(name: string) {
  const encodedName = `${name}=`;
  for (const chunk of document.cookie.split(";")) {
    const item = chunk.trim();
    if (item.startsWith(encodedName)) return item.slice(encodedName.length);
  }
  return null;
}

export function TimeZoneSync() {
  React.useEffect(() => {
    const timeZone = getBrowserTimeZone();
    if (readCookie(TIMEZONE_COOKIE_NAME) === timeZone) return;

    document.cookie = `${TIMEZONE_COOKIE_NAME}=${timeZone}; Path=/; Max-Age=31536000; SameSite=Lax`;
  }, []);

  return null;
}

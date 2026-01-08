import { NextResponse } from "next/server";
import { cookies } from "next/headers";

import { SESSION_COOKIE_NAME } from "@/lib/auth";
import { buildBackendUrl } from "@/lib/backend";

export async function GET() {
  const token = (await cookies()).get(SESSION_COOKIE_NAME)?.value;
  if (!token) return NextResponse.json({ message: "Unauthorized" }, { status: 401 });

  const upstream = await fetch(buildBackendUrl("/auth/me"), {
    headers: { authorization: `Bearer ${token}` },
    cache: "no-store"
  });

  const json: unknown = await upstream.json().catch(() => null);
  if (!upstream.ok) {
    const message =
      json && typeof json === "object" && "detail" in json
        ? String((json as { detail?: unknown }).detail ?? "Unauthorized")
        : "Unauthorized";
    return NextResponse.json({ message }, { status: upstream.status });
  }

  return NextResponse.json(json);
}

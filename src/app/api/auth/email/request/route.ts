import { NextResponse } from "next/server";
import { z } from "zod";

import { buildBackendUrl } from "@/lib/backend";

const schema = z.object({
  email: z.string().trim().email(),
  purpose: z.string().trim().optional()
});

export async function POST(req: Request) {
  const json: unknown = await req.json().catch(() => null);
  const parsed = schema.safeParse(json);
  if (!parsed.success) {
    return NextResponse.json(
      { message: "Invalid payload", issues: parsed.error.issues },
      { status: 400 }
    );
  }

  const upstream = await fetch(buildBackendUrl("/auth/email/request"), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      email: parsed.data.email,
      purpose: parsed.data.purpose ?? "register"
    }),
    cache: "no-store"
  });

  const upstreamJson: unknown = await upstream.json().catch(() => null);
  if (!upstream.ok) {
    const message =
      upstreamJson && typeof upstreamJson === "object" && "detail" in upstreamJson
        ? String((upstreamJson as { detail?: unknown }).detail ?? "Request failed")
        : "Request failed";
    return NextResponse.json({ message }, { status: upstream.status });
  }

  return NextResponse.json({ ok: true, ...((upstreamJson && typeof upstreamJson === "object") ? upstreamJson : {}) });
}


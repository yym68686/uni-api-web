import "server-only";

import { NextResponse } from "next/server";

import { getBackendBaseUrl } from "@/lib/backend";

interface FastApiErrorBody {
  detail?: unknown;
}

async function readJsonSafe(res: Response): Promise<unknown | null> {
  const contentType = res.headers.get("content-type") ?? "";
  if (!contentType.includes("application/json")) return null;
  try {
    return (await res.json()) as unknown;
  } catch {
    return null;
  }
}

export async function proxyToBackend(req: Request, path: string) {
  const baseUrl = getBackendBaseUrl();
  const normalizedPath = path.replace(/^\/+/, "");
  const url = new URL(normalizedPath, `${baseUrl}/`);

  const headers = new Headers();
  const contentType = req.headers.get("content-type");
  if (contentType) headers.set("content-type", contentType);
  const authorization = req.headers.get("authorization");
  if (authorization) headers.set("authorization", authorization);
  const cookie = req.headers.get("cookie");
  if (cookie) headers.set("cookie", cookie);

  const body =
    req.method === "GET" || req.method === "HEAD" ? undefined : await req.text();

  const backendRes = await fetch(url, {
    method: req.method,
    headers,
    body,
    cache: "no-store"
  });

  const json = await readJsonSafe(backendRes);
  if (!backendRes.ok) {
    const detail =
      json && typeof json === "object" && "detail" in json
        ? (json as FastApiErrorBody).detail
        : null;
    let message = "Upstream error";
    if (typeof detail === "string") {
      message = detail;
    } else if (Array.isArray(detail)) {
      const first = detail[0];
      if (first && typeof first === "object" && "msg" in first) {
        const msg = (first as { msg?: unknown }).msg;
        if (typeof msg === "string" && msg.length > 0) message = msg;
      }
    }
    return NextResponse.json({ message }, { status: backendRes.status });
  }

  if (json !== null) {
    return NextResponse.json(json, { status: backendRes.status });
  }

  const text = await backendRes.text();
  return new NextResponse(text, { status: backendRes.status });
}

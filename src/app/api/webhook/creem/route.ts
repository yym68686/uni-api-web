import { buildBackendUrl } from "@/lib/backend";

export async function POST(req: Request) {
  const url = buildBackendUrl("/webhook/creem");
  const body = await req.arrayBuffer();

  const headers = new Headers();
  const signature = req.headers.get("creem-signature");
  if (signature) headers.set("creem-signature", signature);
  const contentType = req.headers.get("content-type");
  if (contentType) headers.set("content-type", contentType);

  const upstream = await fetch(url, {
    method: "POST",
    headers,
    body,
    cache: "no-store"
  });

  const text = await upstream.text();
  const outHeaders = new Headers();
  const upstreamContentType = upstream.headers.get("content-type");
  if (upstreamContentType) outHeaders.set("content-type", upstreamContentType);
  return new Response(text, { status: upstream.status, headers: outHeaders });
}


import { proxyToBackend } from "@/lib/proxy";
import { revalidateTag } from "next/cache";

export function GET(req: Request) {
  return proxyToBackend(req, "/keys");
}

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/keys");
  if (res.ok) revalidateTag("keys:user", { expire: 0 });
  return res;
}

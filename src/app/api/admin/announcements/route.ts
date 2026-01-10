import { proxyToBackend } from "@/lib/proxy";
import { revalidateTag } from "next/cache";

export function GET(req: Request) {
  return proxyToBackend(req, "/announcements");
}

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/admin/announcements");
  if (res.ok) revalidateTag("announcements", { expire: 0 });
  return res;
}

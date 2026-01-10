import { proxyToBackend } from "@/lib/proxy";
import { revalidateTag } from "next/cache";

export async function POST(req: Request) {
  const res = await proxyToBackend(req, "/admin/announcements");
  if (res.ok) revalidateTag("announcements", { expire: 0 });
  return res;
}

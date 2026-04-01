import { proxyToBackend } from "@/lib/proxy";

export function GET(req: Request) {
  const url = new URL(req.url);
  const qs = url.searchParams.toString();
  const path = qs.length > 0 ? `/admin/users?${qs}` : "/admin/users";
  return proxyToBackend(req, path);
}

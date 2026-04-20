import { proxyToBackend } from "@/lib/proxy";

export function GET(req: Request) {
  const url = new URL(req.url);
  const qs = url.searchParams.toString();
  const path = qs.length > 0 ? `/webhook/zhupay?${qs}` : "/webhook/zhupay";
  return proxyToBackend(req, path);
}

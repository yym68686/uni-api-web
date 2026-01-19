import { proxyToBackend } from "@/lib/proxy";

export function GET(req: Request) {
  return proxyToBackend(req, "/announcements");
}


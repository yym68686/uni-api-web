import { proxyToBackend } from "@/lib/proxy";

export function GET(req: Request) {
  return proxyToBackend(req, "/admin/channels");
}

export function POST(req: Request) {
  return proxyToBackend(req, "/admin/channels");
}


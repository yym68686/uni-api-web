import { proxyToBackend } from "@/lib/proxy";

export async function GET(req: Request) {
  return proxyToBackend(req, "/admin/dataocean/status");
}

import { proxyToBackend } from "@/lib/proxy";

export async function GET(req: Request) {
  return proxyToBackend(req, "/admin/settings");
}

export async function PATCH(req: Request) {
  return proxyToBackend(req, "/admin/settings");
}


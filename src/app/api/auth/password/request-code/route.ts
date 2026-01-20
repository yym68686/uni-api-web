import { proxyToBackend } from "@/lib/proxy";

export function POST(req: Request) {
  return proxyToBackend(req, "/auth/password/request-code");
}


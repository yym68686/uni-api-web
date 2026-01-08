import { proxyToBackend } from "@/lib/proxy";

export async function DELETE(req: Request) {
  return proxyToBackend(req, "/account");
}


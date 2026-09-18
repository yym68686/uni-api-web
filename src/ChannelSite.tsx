import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight } from "lucide-react";
import { controlRequest } from "./api";
import { providerId } from "./format";
import type { Channel, Connection } from "./types";
import type { InstalledChannel } from "./sub2apiImports";

export function dashboardURL(base?: string) {
  if (!base) return undefined;
  try {
    const url = new URL(base);
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return undefined;
    return new URL("/dashboard", url.origin).href;
  } catch { return undefined; }
}
export function SiteLink({ base, children }: { base?: string; children: ReactNode }) {
  const url = dashboardURL(base);
  return url ? <a className="site-link" href={url} target="_blank" rel="noopener noreferrer" onClick={(event) => event.stopPropagation()}>{children}<ArrowUpRight size={14} aria-hidden /></a> : <>{children}</>;
}
export function useChannelSites(connection: Connection, installed: InstalledChannel[]) {
  const query = useQuery({
    queryKey: ["channel-sites", connection.session],
    queryFn: ({ signal }) => controlRequest<{ data: { source_id: string; provider: string; dashboard_url: string }[] }>("/v1/channel-sites", { signal }),
    enabled: !!connection.account,
    staleTime: 60000,
    retry: false,
  });
  const sites = new Map<string, string>();
  for (const item of query.data?.data || []) sites.set(providerId(item), item.dashboard_url);
  for (const item of installed) if (item.base) sites.set(providerId(item), item.base);
  return (row?: Pick<Channel, "source_id" | "provider"> | null) => row ? sites.get(providerId(row)) : undefined;
}

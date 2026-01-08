import { DashboardLayout } from "@/components/app/dashboard-layout";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";

interface AppLayoutProps {
  children: React.ReactNode;
}

export default async function AppLayout({ children }: AppLayoutProps) {
  let userName = "User";
  try {
    const res = await fetch(buildBackendUrl("/auth/me"), {
      cache: "no-store",
      headers: await getBackendAuthHeaders()
    });
    if (res.ok) {
      const json: unknown = await res.json();
      if (json && typeof json === "object" && "email" in json) {
        const email = (json as { email?: unknown }).email;
        if (typeof email === "string" && email.length > 0) userName = email;
      }
    }
  } catch {
    // ignore
  }

  return <DashboardLayout userName={userName}>{children}</DashboardLayout>;
}

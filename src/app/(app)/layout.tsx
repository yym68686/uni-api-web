import { DashboardLayout } from "@/components/app/dashboard-layout";
import { getAppName } from "@/lib/app-config";
import { getCurrentUser } from "@/lib/current-user";

interface AppLayoutProps {
  children: React.ReactNode;
}

export default async function AppLayout({ children }: AppLayoutProps) {
  const appName = getAppName();
  const me = await getCurrentUser();
  const userName = me?.email && me.email.length > 0 ? me.email : "User";
  const userRole = me?.role ?? null;

  return (
    <DashboardLayout appName={appName} userName={userName} userRole={userRole}>
      {children}
    </DashboardLayout>
  );
}

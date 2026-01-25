import { AppSidebar } from "@/components/app/app-sidebar";
import { AppTopbar } from "@/components/app/app-topbar";
import { cookies } from "next/headers";

import { LOCALE_COOKIE_NAME, normalizeLocale, type LocaleMode } from "@/lib/i18n/messages";

interface DashboardLayoutProps {
  children: React.ReactNode;
  userName: string;
  userRole: string | null;
  appName: string;
}

export async function DashboardLayout({ children, userName, userRole, appName }: DashboardLayoutProps) {
  const store = await cookies();
  const cookieValue = store.get(LOCALE_COOKIE_NAME)?.value ?? null;
  const initialLocaleMode: LocaleMode = normalizeLocale(cookieValue) ?? "auto";

  return (
    <div className="min-h-dvh bg-background">
      <AppSidebar appName={appName} userRole={userRole} />
      <div className="flex min-h-dvh min-w-0 flex-col sm:pl-64">
        <AppTopbar appName={appName} userName={userName} userRole={userRole} initialLocaleMode={initialLocaleMode} />
        <main id="main" className="min-w-0 flex-1 p-4 sm:p-6">
          <div className="mx-auto w-full max-w-6xl">{children}</div>
        </main>
      </div>
    </div>
  );
}

import type { Locale } from "@/lib/i18n/messages";
import { getDashboardAnnouncements } from "./dashboard-data";
import { DashboardAnnouncementsClient } from "./dashboard-announcements-client";

interface DashboardAnnouncementsProps {
  locale: Locale;
}

export async function DashboardAnnouncements({ locale }: DashboardAnnouncementsProps) {
  const announcements = await getDashboardAnnouncements();
  return <DashboardAnnouncementsClient locale={locale} initialItems={announcements} />;
}

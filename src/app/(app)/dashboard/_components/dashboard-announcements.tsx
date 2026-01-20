import { getDashboardAnnouncements } from "./dashboard-data";
import { DashboardAnnouncementsClient } from "./dashboard-announcements-client";

export async function DashboardAnnouncements() {
  const announcements = await getDashboardAnnouncements();
  return <DashboardAnnouncementsClient initialItems={announcements} />;
}

import { PageHeader } from "@/components/common/page-header";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";
import { InviteContent, getInviteSummary } from "./_components/invite-content";

export const dynamic = "force-dynamic";

export default async function InvitePage() {
  const [locale, summary] = await Promise.all([getRequestLocale(), getInviteSummary()]);

  return (
    <div className="space-y-6">
      <PageHeader title={t(locale, "invite.title")} description={t(locale, "invite.subtitle")} />
      <InviteContent initialSummary={summary} />
    </div>
  );
}


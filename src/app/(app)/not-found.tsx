import Link from "next/link";
import { FileQuestion } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { getRequestLocale } from "@/lib/i18n/server";
import { t } from "@/lib/i18n/messages";

export default async function NotFound() {
  const locale = await getRequestLocale();

  return (
    <div className="mx-auto w-full max-w-2xl space-y-6 py-10">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <FileQuestion className="h-5 w-5 text-muted-foreground" />
            {t(locale, "common.notFoundTitle")}
          </CardTitle>
          <CardDescription>{t(locale, "common.notFoundDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 sm:flex-row">
          <Button asChild className="rounded-xl">
            <Link href="/dashboard">{t(locale, "common.backToDashboard")}</Link>
          </Button>
          <Button asChild variant="outline" className="rounded-xl bg-transparent border-border/80 hover:bg-background/40">
            <Link href="/">{t(locale, "common.backHome")}</Link>
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}


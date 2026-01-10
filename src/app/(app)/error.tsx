"use client";

import * as React from "react";
import Link from "next/link";
import { AlertTriangle, RefreshCcw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useI18n } from "@/components/i18n/i18n-provider";

interface AppErrorProps {
  error: Error & { digest?: string };
  reset: () => void;
}

export default function AppError({ error, reset }: AppErrorProps) {
  const { t } = useI18n();

  React.useEffect(() => {
    console.error(error);
  }, [error]);

  return (
    <div className="mx-auto w-full max-w-2xl space-y-6 py-10">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <AlertTriangle className="h-5 w-5 text-warning" />
            {t("common.unexpectedError")}
          </CardTitle>
          <CardDescription>{t("common.unexpectedErrorDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center">
          <Button
            onClick={reset}
            className="rounded-xl"
          >
            <RefreshCcw className="h-4 w-4" />
            {t("common.tryAgain")}
          </Button>
          <Button asChild variant="outline" className="rounded-xl bg-transparent border-border/80 hover:bg-background/40">
            <Link href="/dashboard">{t("common.backToDashboard")}</Link>
          </Button>
          <Button asChild variant="ghost" className="rounded-xl">
            <Link href="/">{t("common.backHome")}</Link>
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

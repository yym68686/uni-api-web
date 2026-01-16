"use client";

import * as React from "react";
import { ReceiptText } from "lucide-react";

import { ClientDateTime } from "@/components/common/client-datetime";
import { useI18n } from "@/components/i18n/i18n-provider";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import type { BillingLedgerItem } from "@/lib/types";

interface BillingTableClientProps {
  initialItems: BillingLedgerItem[];
  locale: string;
  className?: string;
}

function typeLabelKey(type: string) {
  if (type === "adjustment") return "billing.type.adjustment" as const;
  if (type === "usage_charge") return "billing.type.usageCharge" as const;
  if (type === "refund") return "billing.type.refund" as const;
  if (type === "top_up") return "billing.type.topUp" as const;
  if (type === "referral_bonus") return "billing.type.referralBonus" as const;
  return "billing.type.unknown" as const;
}

export function BillingTableClient({ initialItems, locale, className }: BillingTableClientProps) {
  const { t } = useI18n();
  const items = initialItems;
  const currencyFormatter = React.useMemo(() => {
    return new Intl.NumberFormat(locale, {
      style: "currency",
      currency: "USD",
      currencyDisplay: "narrowSymbol",
      minimumFractionDigits: 2,
      maximumFractionDigits: 2
    });
  }, [locale]);

  return (
    <Card className={className}>
      <CardHeader className="flex flex-row items-center justify-between gap-3">
        <div className="space-y-1">
          <CardTitle>{t("billing.card.title")}</CardTitle>
          <div className="text-sm text-muted-foreground">{t("billing.card.desc")}</div>
        </div>
      </CardHeader>
      <CardContent className="p-0">
        {items.length === 0 ? (
          <div className="p-6">
            <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-border bg-background/40 px-6 py-10 text-center">
              <div className="flex h-12 w-12 items-center justify-center rounded-2xl border border-border bg-muted/40">
                <ReceiptText className="h-5 w-5 text-muted-foreground" />
              </div>
              <div className="mt-4 text-sm font-medium text-foreground">{t("billing.empty.title")}</div>
              <div className="mt-1 text-sm text-muted-foreground">{t("billing.empty.desc")}</div>
            </div>
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("billing.table.time")}</TableHead>
                <TableHead>{t("billing.table.type")}</TableHead>
                <TableHead className="text-right">{t("billing.table.change")}</TableHead>
                <TableHead className="text-right">{t("billing.table.balance")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((row) => {
                const delta = Number(row.deltaUsd ?? 0);
                return (
                  <TableRow key={row.id} className="hover:bg-muted/50">
                    <TableCell className="font-mono tabular-nums">
                      <ClientDateTime value={row.createdAt} locale={locale} timeStyle="medium" />
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">{t(typeLabelKey(row.type))}</Badge>
                    </TableCell>
                    <TableCell
                      className={cn(
                        "text-right font-mono tabular-nums",
                        delta > 0 ? "text-success" : delta < 0 ? "text-destructive" : "text-muted-foreground"
                      )}
                    >
                      {delta > 0 ? "+" : ""}
                      {currencyFormatter.format(delta)}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {currencyFormatter.format(Number(row.balanceUsd ?? 0))}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

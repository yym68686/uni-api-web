"use client";

import * as React from "react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis
} from "recharts";

import type { DailyUsagePoint } from "@/lib/types";
import { formatCompactNumber } from "@/lib/format";
import { cn } from "@/lib/utils";
import { useI18n } from "@/components/i18n/i18n-provider";

interface UsageAreaChartProps {
  data: DailyUsagePoint[];
  className?: string;
}

interface TooltipPayloadItem {
  payload?: DailyUsagePoint;
}

interface TooltipProps {
  active?: boolean;
  payload?: TooltipPayloadItem[];
  label?: string;
}

function UsageTooltip({ active, payload, label }: TooltipProps) {
  const { t } = useI18n();
  if (!active || !payload?.length || !label) return null;
  const point = payload[0]?.payload;
  if (!point) return null;

  return (
    <div
      className={cn(
        "rounded-xl border border-border bg-background/70 text-xs text-foreground",
        "backdrop-blur-md shadow-lg",
        "px-3 py-2"
      )}
    >
      <div className="font-mono tabular-nums text-muted-foreground">{label}</div>
      <div className="mt-2 grid grid-cols-[1fr_auto] gap-x-5 gap-y-1.5">
        <div className="flex items-center gap-2 text-muted-foreground">
          <span className="h-1.5 w-1.5 rounded-full bg-primary shadow-[0_0_0_3px_oklch(var(--primary)/0.12)]" />
          {t("chart.tooltip.requests")}
        </div>
        <div className="text-right font-mono tabular-nums">
          {formatCompactNumber(point.requests)}
        </div>
        <div className="flex items-center gap-2 text-muted-foreground">
          <span className="h-1.5 w-1.5 rounded-full bg-foreground/80 shadow-[0_0_0_3px_oklch(var(--foreground)/0.08)]" />
          {t("chart.tooltip.tokens")}
        </div>
        <div className="text-right font-mono tabular-nums">
          {formatCompactNumber(point.totalTokens)}
        </div>
      </div>
    </div>
  );
}

export function UsageAreaChart({ data, className }: UsageAreaChartProps) {
  const gradientId = React.useId();
  const strokeStyle = React.useMemo<React.CSSProperties>(() => {
    return {
      filter: "drop-shadow(0 0 8px oklch(var(--primary)/0.35))"
    };
  }, []);

  return (
    <div className={cn("h-72 w-full", className)}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 16, right: 12, left: -8, bottom: 0 }}>
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="oklch(var(--primary))" stopOpacity={0.35} />
              <stop offset="100%" stopColor="oklch(var(--primary))" stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="oklch(var(--border))" opacity={0.35} />
          <XAxis
            dataKey="date"
            tickLine={false}
            axisLine={false}
            tickMargin={10}
            minTickGap={24}
            stroke="oklch(var(--muted-foreground))"
            fontSize={12}
          />
          <YAxis
            tickLine={false}
            axisLine={false}
            tickMargin={10}
            width={42}
            stroke="oklch(var(--muted-foreground))"
            fontSize={12}
            tickFormatter={formatCompactNumber}
          />
          <Tooltip
            content={<UsageTooltip />}
            cursor={{
              stroke: "oklch(var(--border))",
              strokeWidth: 1,
              strokeDasharray: "4 4"
            }}
          />
          <Area
            type="monotone"
            dataKey="requests"
            stroke="oklch(var(--primary))"
            strokeWidth={2}
            style={strokeStyle}
            fill={`url(#${gradientId})`}
            dot={false}
            activeDot={{ r: 4 }}
          />
          <Area type="monotone" dataKey="totalTokens" hide />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

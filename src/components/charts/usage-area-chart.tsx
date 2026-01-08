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
  if (!active || !payload?.length || !label) return null;
  const point = payload[0]?.payload;
  if (!point) return null;

  return (
    <div className="rounded-xl border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md">
      <div className="font-mono text-muted-foreground">{label}</div>
      <div className="mt-1 grid grid-cols-2 gap-x-4 gap-y-1">
        <div className="text-muted-foreground">Requests</div>
        <div className="text-right font-mono">
          {formatCompactNumber(point.requests)}
        </div>
        <div className="text-muted-foreground">Tokens</div>
        <div className="text-right font-mono">
          {formatCompactNumber(point.totalTokens)}
        </div>
      </div>
    </div>
  );
}

export function UsageAreaChart({ data, className }: UsageAreaChartProps) {
  const gradientId = React.useId();

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
          <Tooltip content={<UsageTooltip />} cursor={{ stroke: "oklch(var(--border))", strokeWidth: 1 }} />
          <Area
            type="monotone"
            dataKey="requests"
            stroke="oklch(var(--primary))"
            strokeWidth={2}
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

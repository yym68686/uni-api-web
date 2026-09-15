export interface ActualCostRange {
  supported: boolean;
  startDate?: string;
  endDate?: string;
}

const isoDate = (date: Date) => date.toISOString().slice(0, 10);

function addDays(date: Date, days: number) {
  const next = new Date(date);
  next.setUTCDate(next.getUTCDate() + days);
  return next;
}

/**
 * Sub2API reports actual deductions at calendar-day granularity. Returning
 * unsupported for rolling short windows prevents a full day's cost from being
 * presented as a 5m/15m/1h value.
 */
export function actualCostRange(window: string, now = new Date()): ActualCostRange {
  const end = isoDate(now);
  switch (window) {
    case "today":
      return { supported: true, startDate: end, endDate: end };
    case "7d":
      return { supported: true, startDate: isoDate(addDays(now, -6)), endDate: end };
    case "30d":
      return { supported: true, startDate: isoDate(addDays(now, -29)), endDate: end };
    case "week": {
      const day = now.getUTCDay() || 7;
      return { supported: true, startDate: isoDate(addDays(now, 1 - day)), endDate: end };
    }
    case "month":
      return { supported: true, startDate: `${now.getUTCFullYear()}-${String(now.getUTCMonth() + 1).padStart(2, "0")}-01`, endDate: end };
    case "year":
      return { supported: true, startDate: `${now.getUTCFullYear()}-01-01`, endDate: end };
    case "all":
      return { supported: true, startDate: "1970-01-01", endDate: end };
    default:
      return { supported: false };
  }
}

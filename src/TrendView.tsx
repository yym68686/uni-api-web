import type { ReactNode } from "react";
import { ChartNoAxesCombined, Table2 } from "./icons";
import "./trendView.css";

export type TrendMode = "chart" | "table";

export function TrendViewSwitch({
  value,
  onChange,
}: {
  value: TrendMode;
  onChange: (mode: TrendMode) => void;
}) {
  return (
    <div className="trend-view-switch" role="group" aria-label="趋势展示方式">
      <button
        type="button"
        aria-pressed={value === "chart"}
        onClick={() => onChange("chart")}
      >
        <ChartNoAxesCombined size={14} aria-hidden="true" /> 图表
      </button>
      <button
        type="button"
        aria-pressed={value === "table"}
        onClick={() => onChange("table")}
      >
        <Table2 size={14} aria-hidden="true" /> 数据表
      </button>
    </div>
  );
}

export function TrendDataTable({
  label,
  columns,
  children,
}: {
  label: string;
  columns: readonly string[];
  children: ReactNode;
}) {
  return (
    <div
      className="trend-data-scroll"
      tabIndex={0}
      role="region"
      aria-label={`${label}滚动区域`}
    >
      <table className="trend-data-table">
        <caption className="sr-only">{label}</caption>
        <thead>
          <tr>
            {columns.map((column) => (
              <th key={column} scope="col">
                {column}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

import "./loadingIndicator.css";

/** A busy region with no visible copy. Keep actionable states outside it. */
export function LoadingIndicator({
  label = "正在加载",
  size = "section",
  className,
}: {
  label?: string;
  size?: "inline" | "compact" | "section" | "panel" | "fill";
  className?: string;
}) {
  const Element = size === "inline" ? "span" : "div";
  return <Element className={`loading-indicator loading-indicator--${size}${className ? ` ${className}` : ""}`}
    role="status" aria-label={label}>
    <span className="spinner" aria-hidden="true" />
  </Element>;
}

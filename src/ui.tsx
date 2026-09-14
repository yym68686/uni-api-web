import type { ReactNode } from "react";
import * as Tooltip from "@radix-ui/react-tooltip";
import { Activity } from "lucide-react";
export function Tip({
  children,
  text,
}: {
  children: ReactNode;
  text: ReactNode;
}) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger asChild>
        <span className="tip-target" tabIndex={0}>
          {children}
        </span>
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content className="tooltip" sideOffset={8}>
          {text}
          <Tooltip.Arrow className="tooltip-arrow" />
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}
export function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <div className="brand">
      <span className="brand-mark">
        <Activity size={23} strokeWidth={2.4} />
      </span>
      {!compact && (
        <span>
          uni-api<span className="brand-label">CONSOLE</span>
        </span>
      )}
    </div>
  );
}
export function Spinner({ small = false }: { small?: boolean }) {
  return (
    <span aria-label="正在加载" className={`spinner ${small ? "small" : ""}`} />
  );
}
export function Empty({
  title,
  children,
  icon,
}: {
  title: string;
  children: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="empty">
      <div className="empty-icon">{icon || <Activity size={25} />}</div>
      <h3>{title}</h3>
      <p>{children}</p>
    </div>
  );
}

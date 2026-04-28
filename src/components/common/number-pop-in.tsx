import type { CSSProperties, HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

type DigitStyle = CSSProperties & {
  "--uai-digit-index"?: number;
};

export interface NumberPopInProps extends HTMLAttributes<HTMLSpanElement> {
  value: string;
  animate?: boolean;
  staggerCount?: number;
  digitClassName?: string;
}

export function NumberPopIn({
  value,
  animate = true,
  staggerCount = 2,
  className,
  digitClassName,
  "aria-label": ariaLabel,
  ...props
}: NumberPopInProps) {
  const chars = Array.from(value);
  const safeStaggerCount = Math.max(0, Math.floor(staggerCount));
  const staggerStart = Math.max(chars.length - safeStaggerCount, 0);

  return (
    <span
      className={cn("uai-digit-group", animate ? "is-animating" : null, className)}
      aria-label={ariaLabel ?? value}
      {...props}
    >
      {chars.map((char, index) => {
        const digitIndex = index < staggerStart ? 0 : index - staggerStart + 1;
        const style: DigitStyle = { "--uai-digit-index": digitIndex };

        return (
          <span
            key={`${value}-${index}-${char}`}
            className={cn("uai-digit", digitClassName)}
            style={style}
            aria-hidden="true"
          >
            {char}
          </span>
        );
      })}
    </span>
  );
}

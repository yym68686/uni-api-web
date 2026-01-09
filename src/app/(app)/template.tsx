"use client";

import * as React from "react";
import { usePathname } from "next/navigation";

interface TemplateProps {
  children: React.ReactNode;
}

export default function Template({ children }: TemplateProps) {
  const pathname = usePathname();

  return (
    <div key={pathname} className="uai-page-transition">
      {children}
    </div>
  );
}


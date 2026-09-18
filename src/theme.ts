import { useLayoutEffect, useState } from "react";

export type Theme = "light" | "dark";

export function loadTheme(): Theme {
  try {
    return localStorage.getItem("uni-console-theme") === "dark"
      ? "dark"
      : "light";
  } catch {
    return "light";
  }
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(loadTheme);
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = theme;
    document
      .querySelector('meta[name="theme-color"]')
      ?.setAttribute("content", theme === "dark" ? "#101916" : "#f6f7f8");
    try {
      localStorage.setItem("uni-console-theme", theme);
    } catch {
      // Theme selection still works when storage is unavailable.
    }
  }, [theme]);
  return [theme, setTheme] as const;
}

/// <reference types="vite/client" />
import html from "../index.html?raw";
import { act, renderHook } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useTheme } from "./theme";

const bootstrap = new Function(
  "document",
  "localStorage",
  html.match(/<script>([\s\S]*?)<\/script>/)![1],
);

function themeMeta() {
  let meta = document.querySelector<HTMLMetaElement>(
    'meta[name="theme-color"]',
  );
  if (!meta) {
    meta = document.createElement("meta");
    meta.name = "theme-color";
    document.head.append(meta);
  }
  return meta;
}

it.each(["light", "dark"] as const)(
  "applies the saved %s theme before the app starts and preserves it at mount",
  (theme) => {
    localStorage.setItem("uni-console-theme", theme);
    const meta = themeMeta();
    document.documentElement.removeAttribute("data-theme");
    bootstrap(document, localStorage);
    expect(document.documentElement.dataset.theme).toBe(theme);
    expect(meta.content).toBe(theme === "dark" ? "#101916" : "#f6f7f8");
    const { result } = renderHook(useTheme);
    expect(result.current[0]).toBe(theme);
    const next = theme === "dark" ? "light" : "dark";
    act(() => result.current[1](next));
    expect(document.documentElement.dataset.theme).toBe(next);
    expect(localStorage.getItem("uni-console-theme")).toBe(next);
    expect(meta.content).toBe(next === "dark" ? "#101916" : "#f6f7f8");
  },
);

it("keeps the startup and app themes usable when storage is blocked", () => {
  themeMeta();
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  bootstrap(document, localStorage);
  expect(document.documentElement.dataset.theme).toBe("light");
  const { result } = renderHook(useTheme);
  act(() => result.current[1]("dark"));
  expect(document.documentElement.dataset.theme).toBe("dark");
});

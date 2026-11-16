import { afterEach, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

it.each(["chrome", "browser"])(
  "requests optional cookies permission from a user click using %s",
  async (namespace) => {
    vi.resetModules();
    document.body.innerHTML =
      '<button id="enable"></button><p id="status"></p><button id="console"></button>';
    const request = vi.fn(async () => true);
    const create = vi.fn(async () => {});
    const api = { permissions: { request }, tabs: { create } };
    vi.stubGlobal("chrome", namespace === "chrome" ? api : {});
    if (namespace === "browser") vi.stubGlobal("browser", api);
    await import("./popup.js");
    expect(request).not.toHaveBeenCalled();
    document.querySelector("#enable").click();
    await vi.waitFor(() =>
      expect(document.querySelector("#status").textContent).toContain("已授权"),
    );
    expect(request).toHaveBeenCalledExactlyOnceWith({
      permissions: ["cookies"],
    });
    document.querySelector("#console").click();
    expect(create).toHaveBeenCalledExactlyOnceWith({
      url: "https://uni-api-console.fugue.pro",
    });
    request.mockResolvedValueOnce(false);
    document.querySelector("#enable").click();
    await vi.waitFor(() =>
      expect(document.querySelector("#status").textContent).toContain(
        "尚未授权",
      ),
    );
    request.mockRejectedValueOnce(Error("denied"));
    document.querySelector("#enable").click();
    await vi.waitFor(() =>
      expect(document.querySelector("#status").textContent).toContain(
        "授权失败",
      ),
    );
  },
);

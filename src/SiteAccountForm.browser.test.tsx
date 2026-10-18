import { afterEach, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AccountForm } from "./SiteAccountForm";

afterEach(() => vi.unstubAllGlobals());
it.each([true, false])(
  "negotiates New API helper support and forwards only the matching browser session (%s)",
  async (supported) => {
    const writes: any[] = [];
    const logins: any[] = [];
    const onMessage = (event: MessageEvent) => {
      const { channel, id, type, input } = event.data || {};
      if (channel !== "uni-api-browser-login-v1") return;
      if (type === "login") logins.push(input);
      const data =
        type === "ping"
          ? {
              channel,
              id,
              type: "ready",
              ...(supported ? { capabilities: ["sub2api", "newapi"] } : {}),
            }
          : type === "login"
            ? {
                channel,
                id,
                type: "result",
                auth: {
                  access_token: "",
                  refresh_token: "",
                  browser_kind: "newapi",
                  browser_user_id: 42,
                  browser_username: "me",
                  browser_cookies: { session: "signed-session" },
                },
              }
            : null;
      if (data)
        window.dispatchEvent(
          new MessageEvent("message", {
            data,
            source: window,
            origin: location.origin,
          }),
        );
    };
    window.addEventListener("message", onMessage);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url, init) => {
        writes.push(JSON.parse(init.body));
        return Response.json(
          writes.length === 1
            ? {
                requires_browser: true,
                browser_kind: "newapi",
                browser_base: "https://site.test",
              }
            : { id: "new", queued: true },
        );
      }),
    );
    try {
      const saved = vi.fn();
      const user = userEvent.setup();
      render(<AccountForm initial={null} close={() => {}} saved={saved} />);
      await user.type(screen.getByLabelText("站点地址"), "https://site.test");
      await user.type(screen.getByLabelText("用户名 / 邮箱"), "me@example.com");
      await user.type(screen.getByLabelText("账号密码"), "private-password");
      await user.click(screen.getByRole("button", { name: "连接并检测" }));
      await user.click(
        await screen.findByRole("button", { name: "使用浏览器登录" }),
      );
      if (supported) {
        await waitFor(() => expect(saved).toHaveBeenCalledOnce());
        expect(logins[0]).toMatchObject({
          kind: "newapi",
          base: "https://site.test",
          email: "me@example.com",
        });
        expect(writes[1]).toMatchObject({
          browser_kind: "newapi",
          access_token: "",
          browser_user_id: 42,
          browser_cookies: { session: "signed-session" },
        });
      } else {
        expect(await screen.findByRole("alert")).toHaveTextContent("1.2.0");
        expect(logins).toHaveLength(0);
        expect(writes).toHaveLength(1);
        expect(
          screen.getByRole("link", { name: "下载新版登录助手" }),
        ).toHaveAttribute("href", "/uni-api-browser-helper.zip");
      }
    } finally {
      window.removeEventListener("message", onMessage);
    }
  },
);

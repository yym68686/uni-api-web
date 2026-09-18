import { expect, it } from "vitest";
import { loginWithBrowser } from "./sub2apiBrowser";

it("rejects forged cross-origin replies and cancels pending login", async () => {
  const abort = new AbortController();
  const request = new Promise<any>((resolve) => {
    const listener = (event: MessageEvent) => {
      if (event.data?.type === "login") {
        window.removeEventListener("message", listener);
        resolve(event.data);
      }
    };
    window.addEventListener("message", listener);
  });
  const login = loginWithBrowser(
    {
      base: "https://site.example",
      email: "me@example.com",
      password: "secret",
      agreed: false,
    },
    abort.signal,
  );
  const body = await request;
  window.dispatchEvent(
    new MessageEvent("message", {
      origin: "https://evil.example",
      source: window,
      data: {
        channel: body.channel,
        id: body.id,
        type: "result",
        auth: { access_token: "forged" },
      },
    }),
  );
  const rejected = expect(login).rejects.toThrow("已取消");
  abort.abort();
  await rejected;
});

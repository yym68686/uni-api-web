import { expect, it, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ChannelControls, inheritedDisabled } from "./ChannelControls";
import type { ControlState } from "./ChannelControls";

afterEach(() => vi.unstubAllGlobals());
it("stages scoped edits, saves through source API, and restores manually", async () => {
  let state: ControlState = {
    revision: "boot:0",
    instance_id: "boot",
    config_revision: "config",
    rules: [],
    reset_on_restart: true,
  };
  const mutations: any[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const u = new URL(input, "https://console.test");
      let body: any;
      if (u.pathname.endsWith("channel-controls")) {
        if (init?.method === "POST") {
          const data = JSON.parse(init.body as string);
          mutations.push(data);
          state = {
            ...state,
            revision: "boot:" + mutations.length,
            rules:
              data.action === "reset"
                ? []
                : [
                    {
                      api_key_id: data.api_key_id,
                      model: data.model,
                      order: data.order,
                      disabled: data.disabled,
                    },
                  ],
          };
        }
        body = state;
      } else if (u.pathname.endsWith("api-keys"))
        body = {
          data: [{ key_id: "do::key-one", prefix: "masked", position: 1 }],
        };
      else
        body = {
          data: ["first", "second"].map((provider) => ({
            provider,
            model: "m",
          })),
          snapshot_revision: "config",
        };
      return new Response(JSON.stringify(body));
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const user = userEvent.setup();
  const app = render(
    <QueryClientProvider client={client}>
      <ChannelControls
        connection={{
          base: "https://console.test",
          key: "",
          account: true,
          session: "test",
          sourceId: "do",
        }}
        sources={[
          {
            id: "do",
            name: "DigitalOcean",
            base: "https://gateway.test",
            created_at: 0,
            has_storage: true,
          },
        ]}
        initialKey=""
        initialModel=""
        onApplied={() => {}}
      />
    </QueryClientProvider>,
  );
  await screen.findByLabelText("临时停用 first");
  await user.selectOptions(
    screen.getByLabelText("控制 API key"),
    "do::key-one",
  );
  await user.selectOptions(screen.getByLabelText("控制模型"), "m");
  await screen.findByLabelText("临时停用 first");
  await user.click(screen.getByLabelText("上移 second"));
  await user.click(screen.getByLabelText("临时停用 first"));
  expect(mutations).toHaveLength(0);
  await user.click(screen.getByRole("button", { name: "应用临时修改" }));
  await waitFor(() => expect(mutations).toHaveLength(1));
  expect(mutations[0]).toEqual({
    revision: "boot:0",
    action: "set",
    api_key_id: "key-one",
    model: "m",
    order: ["second", "first"],
    disabled: ["first"],
  });
  await screen.findByRole("button", { name: "恢复此规则" });
  await user.click(screen.getByRole("button", { name: "恢复此规则" }));
  await waitFor(() => expect(state.rules).toHaveLength(0));
  expect(mutations[1].action).toBe("reset");
  expect(mutations[1].revision).toBe("boot:1");
  expect(screen.getByText(/无到期时间/)).toBeInTheDocument();
  app.unmount();
  client.clear();
});
it("global disables cannot be lifted by a narrower rule or leak to other scopes", () => {
  const rules = [
    { api_key_id: "", model: "", order: [], disabled: ["global"] },
    { api_key_id: "k", model: "m", order: [], disabled: ["specific"] },
  ];
  expect(inheritedDisabled(rules, "k", "m", "global")).toBe(true);
  expect(inheritedDisabled(rules, "other", "other", "specific")).toBe(false);
  expect(inheritedDisabled(rules, "k", "m", "specific")).toBe(false);
});

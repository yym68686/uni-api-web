import { expect, it, vi } from "vitest";
import { render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PriceSettings } from "./PriceSettings";
import { MODEL_PRICE_CATALOG } from "./modelPrices";
import type { ModelPrice } from "./types";

it("defaults GPT write billing off and other models on, saves explicit choices without erasing rates", async () => {
  const saved: ModelPrice[] = [];
  vi.stubGlobal("fetch", vi.fn(async (_url, options) => {
    saved.push(JSON.parse(options.body));
    return Response.json({ price: saved.at(-1) });
  }));
  const props = { prices: [] as ModelPrice[], loading: false, connection: { base: "", key: "", session: "test", account: true }, onSaved: vi.fn() };
  const view = render(<PriceSettings {...props} />);
  for (const price of MODEL_PRICE_CATALOG) {
    expect(screen.getByRole("checkbox", { name: `${price.model} 计算缓存写入费用` })).toHaveProperty("checked", !price.model.startsWith("gpt-"));
  }
  const user = userEvent.setup();
  for (const model of ["gpt-6-astra", "claude-fable-5"]) {
    const toggle = screen.getByRole("checkbox", { name: `${model} 计算缓存写入费用` });
    await user.click(toggle);
    await user.click(within(toggle.closest("article")!).getByRole("button", { name: "保存价格" }));
  }
  await waitFor(() => expect(props.onSaved).toHaveBeenCalledTimes(2));
  expect(saved[0]).toMatchObject({ model: "gpt-6-astra", charge_cache_write: true, cache_write: 12.5, cache_read: 1 });
  expect(saved[1]).toMatchObject({ model: "claude-fable-5", charge_cache_write: false, cache_write: 12.5, cache_write_1h: 20, cache_read: 1 });
  view.unmount();
  render(<PriceSettings {...props} prices={saved} />);
  expect(screen.getByRole("checkbox", { name: "gpt-6-astra 计算缓存写入费用" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "claude-fable-5 计算缓存写入费用" })).not.toBeChecked();
});

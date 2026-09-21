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
    expect(screen.getByRole("checkbox", { name: `${price.model} 计算缓存写入费用` })).toHaveProperty("checked", !/^(gpt|jev)-/.test(price.model));
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

it("offers independent sales percentages with family defaults and persists decimal and zero values", async () => {
  const saved: ModelPrice[]=[];
  vi.stubGlobal("fetch",vi.fn(async (_url,options)=>{saved.push(JSON.parse(options.body));return Response.json({price:saved.at(-1)})}));
  const props={prices:[] as ModelPrice[],loading:false,connection:{base:"",key:"",session:"test",account:true},onSaved:vi.fn()};
  const view=render(<PriceSettings {...props}/>);
  expect(screen.getByLabelText("gpt-6-astra 售卖价格百分比")).toHaveValue(2.5);
  expect(screen.getByLabelText("claude-fable-5 售卖价格百分比")).toHaveValue(15);
  expect(screen.getByLabelText("gemini-3.1-pro 售卖价格百分比")).toHaveValue(15);
  const user=userEvent.setup();
  for(const [model,value] of [["gpt-6-astra","0"],["claude-fable-5","18.75"],["gemini-3.1-pro","9"]]){
   const input=screen.getByLabelText(`${model} 售卖价格百分比`);await user.clear(input);
   expect(within(input.closest("article")!).getByRole("button",{name:"保存价格"})).toBeDisabled();
   await user.type(input,value);await user.click(within(input.closest("article")!).getByRole("button",{name:"保存价格"}));
  }
  await waitFor(()=>expect(props.onSaved).toHaveBeenCalledTimes(3));
  expect(saved.map(p=>p.sale_percent)).toEqual([0,18.75,9]);
  view.unmount();render(<PriceSettings {...props} prices={saved}/>);
  expect(screen.getByLabelText("gpt-6-astra 售卖价格百分比")).toHaveValue(0);
  expect(screen.getByLabelText("claude-fable-5 售卖价格百分比")).toHaveValue(18.75);
  expect(screen.getByLabelText("gemini-3.1-pro 售卖价格百分比")).toHaveValue(9);
});

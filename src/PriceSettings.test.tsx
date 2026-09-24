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
    await user.click(within(toggle.closest("tr")!).getByRole("button", { name: "保存价格" }));
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
   expect(within(input.closest("tr")!).getByRole("button",{name:"保存价格"})).toBeDisabled();
   await user.type(input,value);await user.click(within(input.closest("tr")!).getByRole("button",{name:"保存价格"}));
  }
  await waitFor(()=>expect(props.onSaved).toHaveBeenCalledTimes(3));
  expect(saved.map(p=>p.sale_percent)).toEqual([0,18.75,9]);
  view.unmount();render(<PriceSettings {...props} prices={saved}/>);
  expect(screen.getByLabelText("gpt-6-astra 售卖价格百分比")).toHaveValue(0);
  expect(screen.getByLabelText("claude-fable-5 售卖价格百分比")).toHaveValue(18.75);
  expect(screen.getByLabelText("gemini-3.1-pro 售卖价格百分比")).toHaveValue(9);
});

it("keeps aligned table columns, searchable models and accessible pricing details", async () => {
  const props = { prices: [] as ModelPrice[], loading: false, connection: { base: "", key: "", session: "test", account: true }, onSaved: vi.fn() };
  render(<PriceSettings {...props} />);
  const table = screen.getByRole("table", { name: "模型价格" });
  expect(within(table).getAllByRole("columnheader")).toHaveLength(10);
  expect(within(table).getAllByRole("rowheader")).toHaveLength(MODEL_PRICE_CATALOG.length);
  for (const row of table.querySelectorAll("tbody tr")) expect(row.children).toHaveLength(10);
  const user = userEvent.setup();
  await user.type(screen.getByRole("textbox", { name: "搜索模型价格" }), "grok-4.7");
  expect(within(table).getAllByRole("rowheader")).toHaveLength(1);
  expect(screen.getByLabelText("grok-4.7 输入价格")).toHaveValue(2);
  const trigger = screen.getByRole("button", { name: "查看 grok-4.7 价格说明" });
  await user.click(trigger);
  const dialog = screen.getByRole("dialog", { name: "grok-4.7" });
  expect(dialog).toHaveTextContent("≥200K");
  expect(within(dialog).getByRole("link", { name: "查看官方价格来源" })).toHaveAttribute("href", "https://docs.x.ai/developers/pricing");
  await user.keyboard("{Escape}");
  expect(trigger).toHaveFocus();
  await user.clear(screen.getByRole("textbox", { name: "搜索模型价格" }));
  await user.type(screen.getByRole("textbox", { name: "搜索模型价格" }), "codex-auto-review");
  expect(screen.getByLabelText("codex-auto-review 输入价格")).toHaveValue(null);
  expect(screen.getByRole("button", { name: "保存价格" })).toBeDisabled();
  await user.clear(screen.getByRole("textbox", { name: "搜索模型价格" }));
  await user.type(screen.getByRole("textbox", { name: "搜索模型价格" }), "no-such-model");
  expect(within(table).getByRole("cell")).toHaveTextContent("没有匹配的检测模型");
});

it("saves independent TTL prices and derived cache rates from the table", async () => {
  const saved: ModelPrice[] = [];
  vi.stubGlobal("fetch", vi.fn(async (_url, init) => {
    const price = JSON.parse(init.body);
    saved.push(price);
    return Response.json({ price });
  }));
  render(<PriceSettings prices={[]} loading={false} connection={{ base: "", key: "", session: "test", account: true }} onSaved={vi.fn()} />);
  const user = userEvent.setup();
  const edit = async (label: string, value: string) => {
    const input = screen.getByLabelText(label);
    await user.clear(input);
    await user.type(input, value);
  };
  await edit("claude-opus-5-5 缓存写入 · 5 分钟价格", "6.25");
  await edit("claude-opus-5-5 缓存写入 · 1 小时价格", "9.5");
  await user.click(screen.getByLabelText("claude-opus-5-5 确认价格并用于估算"));
  const claude = screen.getByLabelText("claude-opus-5-5 输入价格").closest("tr")!;
  expect(claude).toHaveTextContent("未保存");
  await user.click(within(claude).getByRole("button", { name: "保存价格" }));
  await waitFor(() => expect(saved[0]).toMatchObject({ model: "claude-opus-5-5", cache_write: 6.25, cache_write_1h: 9.5, verified: false }));
  expect(claude).not.toHaveTextContent("未保存");
  await edit("grok-4.7 输入价格", "3.25");
  const grok = screen.getByLabelText("grok-4.7 输入价格").closest("tr")!;
  expect(within(grok).getAllByText("同输入")).toHaveLength(2);
  await user.click(within(grok).getByRole("button", { name: "保存价格" }));
  await waitFor(() => expect(saved[1]).toMatchObject({ model: "grok-4.7", input: 3.25, cache_write: 3.25, cache_write_1h: 3.25 }));
});

it("keeps a failed save on its own row and leaves other prices editable", async () => {
  let respond: (response: Response) => void = () => {};
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => { respond = resolve; })));
  render(<PriceSettings prices={[]} loading={false} connection={{ base: "", key: "", session: "test", account: true }} onSaved={vi.fn()} />);
  const user = userEvent.setup();
  const input = screen.getByLabelText("grok-4.7 输入价格");
  await user.clear(input); await user.type(input, "3");
  const row = input.closest("tr")!;
  await user.click(within(row).getByRole("button", { name: "保存价格" }));
  expect(input).toBeDisabled();
  expect(screen.getByLabelText("gpt-6-sol 输入价格")).toBeEnabled();
  respond(new Response("保存暂不可用", { status: 503 }));
  expect(await screen.findByRole("alert")).toHaveTextContent("grok-4.7");
  expect(row.nextElementSibling).toHaveClass("price-error-row");
  expect(input).toHaveValue(3);
  expect(input).toBeEnabled();
  expect(row).toHaveTextContent("未保存");
});

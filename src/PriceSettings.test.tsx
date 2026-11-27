import { expect, it, vi } from "vitest";
import { fireEvent, render, screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { PriceSettings } from "./PriceSettings";
import { MODEL_PRICE_CATALOG } from "./modelPrices";
import type { ModelPrice } from "./types";

function openEditor(model: string) {
  const current = screen.queryByRole("dialog", {name: model});
  if (current) return;
  const close = screen.queryByRole("button", {name:"关闭价格编辑"});
  if (close) fireEvent.click(close);
  fireEvent.click(screen.getByRole("button", {name:`编辑 ${model}`}));
}
function closeEditor() {
  fireEvent.click(screen.getByRole("button", {name:"关闭价格编辑"}));
}

it("shows a labeled edit action without hover and returns keyboard focus to it on close", async () => {
  render(<PriceSettings prices={[]} loading={false} connection={{ base: "", key: "", session: "test", account: true }} onSaved={vi.fn()} />);
  const user = userEvent.setup();
  const opener = screen.getByRole("button", { name: "编辑 gpt-6-astra" });
  const unpricedOpener = screen.getByRole("button", { name: "编辑 glm-5.4" });
  expect(within(opener).getByText("编辑")).toBeVisible();
  expect(within(unpricedOpener).getByText("编辑")).toBeVisible();

  opener.focus();
  await user.keyboard("{Enter}");
  const editor = screen.getByRole("dialog", { name: "gpt-6-astra" });
  expect(within(editor).getByRole("spinbutton", { name: "gpt-6-astra 输入价格" })).toBeEnabled();
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  await waitFor(() => expect(opener).toHaveFocus());
});

it("shows official GPT 6.2 Standard prices and explains cache retention limits", async () => {
 render(<PriceSettings prices={[]} loading={false} connection={{base:'',key:'',session:'test',account:true}} onSaved={vi.fn()}/>);
 const user=userEvent.setup();
 const search=screen.getByRole('textbox',{name:'搜索模型价格'});
 await user.click(search);await user.paste('gpt-6.2');
 const table=within(screen.getByRole('table',{name:'模型价格'}));
 expect(table.getAllByRole('rowheader')).toHaveLength(2);
 for(const [label,astra,sol] of [['输入',8,2],['输出',40,10],['缓存读取',.4,.1],['缓存写入 · 统一单价',10,2.5]] as const){
  openEditor("gpt-6.2-astra");
  expect(screen.getByLabelText(`gpt-6.2-astra ${label}价格`)).toHaveValue(astra);
  openEditor("gpt-6.2-sol");
  expect(screen.getByLabelText(`gpt-6.2-sol ${label}价格`)).toHaveValue(sol);
 }
 for(const model of ['gpt-6.2-astra','gpt-6.2-sol']){
  openEditor(model);
  expect(screen.getByRole('checkbox',{name:model+' 计算缓存写入费用'})).not.toBeChecked();
  expect(screen.getByLabelText(model+' 售卖价格百分比')).toHaveValue(2.5);
  expect(screen.getByRole('checkbox',{name:model+' 超过272k加价'})).toBeChecked();
 }
 const sol=screen.getByLabelText('gpt-6.2-sol 输入价格').closest('form')!;
 expect(within(sol).getByRole('button',{name:'采用参考价'})).toBeEnabled();
 await user.click(screen.getByRole('button',{name:'查看 gpt-6.2-sol 价格说明'}));
 const dialog=screen.getByRole('dialog',{name:'gpt-6.2-sol'});
 expect(within(dialog).getByText('官方参考价')).toBeVisible();
 expect(dialog).toHaveTextContent('30 分钟 $2.50');
 expect(dialog).toHaveTextContent('12 小时为 $5');
 expect(dialog).toHaveTextContent('不自动套用 12 小时费率');
 expect(within(dialog).getByRole('link',{name:'查看官方价格来源'})).toHaveAttribute('href','https://developers.openai.com/api/docs/pricing');
});

it.each([
 ['gemini-4-hydrogen',1.5,7.5,.075],
 ['gemini-4-neon',3,15,.15],
] as const)('shows %s Standard pricing and implicit caching',async(model,input,output,cacheRead)=>{
 render(<PriceSettings prices={[]} loading={false} connection={{base:'',key:'',session:'test',account:true}} onSaved={vi.fn()}/>);
 const user=userEvent.setup();
 await user.click(screen.getByRole('textbox',{name:'搜索模型价格'}));await user.paste(model);
 openEditor(model);
 for(const [label,value] of [['输入',input],['输出',output],['缓存读取',cacheRead]] as const){
  expect(screen.getByLabelText(`${model} ${label}价格`)).toHaveValue(value);
 }
 expect(within(screen.getByLabelText(model+' 输入价格').closest('form')!).getByText(/缓存写入随输入单价/)).toBeVisible();
 expect(screen.getByRole('checkbox',{name:model+' 超过272k加价'})).not.toBeChecked();
 await user.click(screen.getByRole('button',{name:'查看 '+model+' 价格说明'}));
 const dialog=screen.getByRole('dialog',{name:model});
 expect(within(dialog).getByRole('link',{name:'查看官方价格来源'})).toHaveAttribute('href',`https://aistudio.google.com/docs/pricing#${model}`);
 if(model==='gemini-4-hydrogen') expect(dialog).toHaveTextContent('完整 1M 上下文使用同一单价');
 expect(dialog).toHaveTextContent('不支持显式缓存');
});

it('shows Carbon promotional pricing and its expiration without inventing cache TTL rates',async()=>{
 render(<PriceSettings prices={[]} loading={false} connection={{base:'',key:'',session:'test',account:true}} onSaved={vi.fn()}/>);
 const model='gemini-4-carbon',user=userEvent.setup();
 await user.click(screen.getByRole('textbox',{name:'搜索模型价格'}));await user.paste(model);
 openEditor(model);
 for(const [label,value] of [['输入',2],['输出',10],['缓存读取',.1]] as const){
  expect(screen.getByLabelText(`${model} ${label}价格`)).toHaveValue(value);
 }
 expect(within(screen.getByLabelText(model+' 输入价格').closest('form')!).getByText(/缓存写入随输入单价/)).toBeVisible();
 await user.click(screen.getByRole('button',{name:'查看 '+model+' 价格说明'}));
 const dialog=within(screen.getByRole('dialog',{name:model}));
 expect(dialog.getByRole('link',{name:'查看官方价格来源'})).toHaveAttribute('href','https://ai.google.dev/gemini-api/docs/pricing#gemini-4-carbon');
 expect(screen.getByRole('dialog',{name:model})).toHaveTextContent('2026-12-01 起输入 $4、输出 $20、缓存读取 $0.20');
});

it('shows Fable 5.2 standard and independent cache TTL prices',async()=>{
 render(<PriceSettings prices={[]} loading={false} connection={{base:'',key:'',session:'test',account:true}} onSaved={vi.fn()}/>);
 const model='claude-fable-5-2',user=userEvent.setup();
 await user.click(screen.getByRole('textbox',{name:'搜索模型价格'}));await user.paste(model);
 openEditor(model);
 for(const [label,value] of [['输入',8],['输出',40],['缓存读取',.2],['缓存写入 · 5 分钟',10],['缓存写入 · 1 小时',16]] as const){
  expect(screen.getByLabelText(`${model} ${label}价格`)).toHaveValue(value);
 }
 expect(screen.getByRole('checkbox',{name:model+' 超过272k加价'})).not.toBeChecked();
 await user.click(screen.getByRole('button',{name:'查看 '+model+' 价格说明'}));
 const dialog=within(screen.getByRole('dialog',{name:model}));
 expect(dialog.getByRole('link',{name:'查看官方价格来源'})).toHaveAttribute('href','https://platform.claude.com/docs/en/about-claude/pricing');
 expect(screen.getByRole('dialog',{name:model})).toHaveTextContent('完整 1M 上下文使用同一单价');
});

it("shows GPT 6.1 Terra standard prices and explains optional pricing adjustments",async()=>{
 render(<PriceSettings prices={[]} loading={false} connection={{base:"",key:"",session:"test",account:true}} onSaved={vi.fn()}/>);
 const model="gpt-6.1-terra",user=userEvent.setup();
 await user.click(screen.getByRole("textbox",{name:"搜索模型价格"}));await user.paste(model);
 openEditor(model);
 expect(screen.getByLabelText(model+" 输入价格")).toHaveValue(1);
 expect(screen.getByLabelText(model+" 输出价格")).toHaveValue(5);
 expect(screen.getByLabelText(model+" 缓存读取价格")).toHaveValue(.05);
 expect(screen.getByLabelText(model+" 缓存写入 · 统一单价价格")).toHaveValue(1.25);
 expect(screen.getByRole("checkbox",{name:model+" 超过272k加价"})).not.toBeChecked();
 await user.click(screen.getByRole("button",{name:"查看 "+model+" 价格说明"}));
 const dialog=within(screen.getByRole("dialog",{name:model}));
 expect(dialog.getByRole("link",{name:"查看官方价格来源"})).toHaveAttribute("href","https://developers.openai.com/api/docs/pricing");
 expect(screen.getByRole("dialog",{name:model})).toHaveTextContent("12 小时为 $2.50");
});

it("shows Kimi K3.1 Pro as editable unconfirmed pricing without copying K3 prices",async()=>{
 render(<PriceSettings prices={[]} loading={false} connection={{base:"",key:"",session:"test",account:true}} onSaved={vi.fn()}/>);
 const model="kimi-k3.1-pro",user=userEvent.setup();
 await user.click(screen.getByRole("textbox",{name:"搜索模型价格"}));await user.paste(model);
 openEditor(model);
 expect(screen.getByLabelText(model+" 输入价格")).toHaveValue(null);
 expect(screen.getByLabelText(model+" 输出价格")).toHaveValue(null);
 expect(screen.getByRole("button",{name:"保存价格"})).toBeDisabled();
 await user.click(screen.getByRole("button",{name:"查看 "+model+" 价格说明"}));
 expect(screen.getByRole("dialog",{name:model})).toHaveTextContent("参考单价待确认");
});

it("shows GLM-5.4 as unpriced rather than free", () => {
  const model = "glm-5.4";
  render(<PriceSettings prices={[]} loading={false} connection={{ base: "", key: "", session: "test", account: true }} onSaved={vi.fn()} />);
  fireEvent.change(screen.getByRole("textbox", { name: "搜索模型价格" }), { target: { value: model } });
  expect(screen.getByRole("rowheader", { name: /glm-5\.4/ })).toHaveTextContent("未定价");
  openEditor(model);
  expect(screen.getByLabelText(model + " 输入价格")).toHaveValue(null);
  expect(screen.getByLabelText(model + " 输出价格")).toHaveValue(null);
  expect(screen.getByRole("button", { name: "保存价格" })).toBeDisabled();
});

it("shows DeepSeek Flash 1017 with its reference prices and upstream naming note", async () => {
  render(<PriceSettings prices={[]} loading={false} connection={{ base: "", key: "", session: "test", account: true }} onSaved={vi.fn()} />);
  const model = "deepseek-v4.1-flash-1017";
  const user = userEvent.setup();
  await user.type(screen.getByRole("textbox", { name: "搜索模型价格" }), model);
  expect(within(screen.getByRole("table", { name: "模型价格" })).getAllByRole("rowheader")).toHaveLength(1);
  openEditor(model);
  expect(screen.getByLabelText(model + " 输入价格")).toHaveValue(.3);
  expect(screen.getByLabelText(model + " 输出价格")).toHaveValue(1.2);
  await user.click(screen.getByRole("button", { name: "查看 " + model + " 价格说明" }));
  const dialog = screen.getByRole("dialog", { name: model });
  expect(dialog).toHaveTextContent("deepseek-flash");
  expect(dialog).toHaveTextContent("不自动按时段切价");
});

it("defaults GPT write billing off and other models on, saves explicit choices without erasing rates", async () => {
  const saved: ModelPrice[] = [];
  vi.stubGlobal("fetch", vi.fn(async (_url, options) => {
    saved.push(JSON.parse(options.body));
    return Response.json({ price: saved.at(-1) });
  }));
  const props = { prices: [] as ModelPrice[], loading: false, connection: { base: "", key: "", session: "test", account: true }, onSaved: vi.fn() };
  const view = render(<PriceSettings {...props} />);
  for (const price of MODEL_PRICE_CATALOG.filter(p => ["gpt-6-astra","gpt-6-sol","claude-fable-5","gemini-3.1-pro"].includes(p.model))) {
    openEditor(price.model);
    expect(screen.getByRole("checkbox", { name: `${price.model} 计算缓存写入费用` })).toHaveProperty("checked", !/^(gpt|jev)-/.test(price.model));
  }
  const user = userEvent.setup();
  for (const model of ["gpt-6-astra", "claude-fable-5"]) {
    openEditor(model);
    const toggle = screen.getByRole("checkbox", { name: `${model} 计算缓存写入费用` });
    await user.click(toggle);
    await user.click(within(toggle.closest("form")!).getByRole("button", { name: "保存价格" }));
  }
  await waitFor(() => expect(props.onSaved).toHaveBeenCalledTimes(2));
  expect(saved[0]).toMatchObject({ model: "gpt-6-astra", charge_cache_write: true, cache_write: 12.5, cache_read: 1 });
  expect(saved[1]).toMatchObject({ model: "claude-fable-5", charge_cache_write: false, cache_write: 12.5, cache_write_1h: 20, cache_read: 1 });
  view.unmount();
  render(<PriceSettings {...props} prices={saved} />);
  openEditor("gpt-6-astra");
  expect(screen.getByRole("checkbox", { name: "gpt-6-astra 计算缓存写入费用" })).toBeChecked();
  openEditor("claude-fable-5");
  expect(screen.getByRole("checkbox", { name: "claude-fable-5 计算缓存写入费用" })).not.toBeChecked();
});

it("offers independent sales percentages with family defaults and persists decimal and zero values", async () => {
  const saved: ModelPrice[]=[];
  vi.stubGlobal("fetch",vi.fn(async (_url,options)=>{saved.push(JSON.parse(options.body));return Response.json({price:saved.at(-1)})}));
  const props={prices:[] as ModelPrice[],loading:false,connection:{base:"",key:"",session:"test",account:true},onSaved:vi.fn()};
  const view=render(<PriceSettings {...props}/>);
  openEditor("gpt-6-astra");
  expect(screen.getByLabelText("gpt-6-astra 售卖价格百分比")).toHaveValue(2.5);
  openEditor("claude-fable-5");
  expect(screen.getByLabelText("claude-fable-5 售卖价格百分比")).toHaveValue(15);
  openEditor("gemini-3.1-pro");
  expect(screen.getByLabelText("gemini-3.1-pro 售卖价格百分比")).toHaveValue(15);
  const user=userEvent.setup();
  for(const [model,value] of [["gpt-6-astra","0"],["claude-fable-5","18.75"],["gemini-3.1-pro","9"]]){
   openEditor(model);
   const input=screen.getByLabelText(`${model} 售卖价格百分比`);await user.clear(input);
   expect(within(input.closest("form")!).getByRole("button",{name:"保存价格"})).toBeDisabled();
   await user.type(input,value);await user.click(within(input.closest("form")!).getByRole("button",{name:"保存价格"}));
  }
  await waitFor(()=>expect(props.onSaved).toHaveBeenCalledTimes(3));
  expect(saved.map(p=>p.sale_percent)).toEqual([0,18.75,9]);
  view.unmount();render(<PriceSettings {...props} prices={saved}/>);
  openEditor("gpt-6-astra");
  expect(screen.getByLabelText("gpt-6-astra 售卖价格百分比")).toHaveValue(0);
  openEditor("claude-fable-5");
  expect(screen.getByLabelText("claude-fable-5 售卖价格百分比")).toHaveValue(18.75);
  openEditor("gemini-3.1-pro");
  expect(screen.getByLabelText("gemini-3.1-pro 售卖价格百分比")).toHaveValue(9);
});

it("keeps aligned table columns, searchable models and accessible pricing details", async () => {
  const props = { prices: [] as ModelPrice[], loading: false, connection: { base: "", key: "", session: "test", account: true }, onSaved: vi.fn() };
  render(<PriceSettings {...props} />);
  const table = screen.getByRole("table", { name: "模型价格" });
  expect(screen.getByRole("button", { name: "编辑 gemini-4-argon" })).toHaveTextContent("gemini-4-argon");
  expect(within(table).getAllByRole("columnheader")).toHaveLength(6);
  expect(within(table).getAllByText("美元 / 百万 token")).toHaveLength(3);
  expect(within(table).getAllByRole("rowheader")).toHaveLength(MODEL_PRICE_CATALOG.length);
  for (const row of table.querySelectorAll("tbody tr")) expect(row.children).toHaveLength(6);
  const user = userEvent.setup();
  await user.type(screen.getByRole("textbox", { name: "搜索模型价格" }), "gemini-4-argon");
  expect(await within(table).findAllByRole("rowheader")).toHaveLength(1);
  openEditor("gemini-4-argon");
  expect(screen.getByLabelText("gemini-4-argon 输入价格")).toHaveValue(2);
  const trigger = screen.getByRole("button", { name: "查看 gemini-4-argon 价格说明" });
  await user.click(trigger);
  const dialog = screen.getByRole("dialog", { name: "gemini-4-argon" });
  expect(dialog).toHaveTextContent("2026-11-30");
  expect(within(dialog).getByRole("link", { name: "查看官方价格来源" })).toHaveAttribute("href", "https://ai.google.dev/gemini-api/docs/pricing#gemini-4-argon");
  await user.keyboard("{Escape}");
  expect(trigger).toHaveFocus();
  closeEditor();
  await user.clear(screen.getByRole("textbox", { name: "搜索模型价格" }));
  await user.type(screen.getByRole("textbox", { name: "搜索模型价格" }), "codex-auto-review");
  await screen.findByRole("button",{name:"编辑 codex-auto-review"});
  openEditor("codex-auto-review");
  expect(screen.getByLabelText("codex-auto-review 输入价格")).toHaveValue(null);
  expect(screen.getByRole("button", { name: "保存价格" })).toBeDisabled();
  closeEditor();
  await user.clear(screen.getByRole("textbox", { name: "搜索模型价格" }));
  await user.type(screen.getByRole("textbox", { name: "搜索模型价格" }), "no-such-model");
  expect(within(table).getByRole("cell")).toHaveTextContent("没有匹配的模型");
});

it("chooses price data columns in the filter, persists the choice and restores the defaults", async () => {
  const key = "uni-console:price-columns:v2";
  const props = { prices: [], loading: false, connection: { base: "", key: "", session: "test", account: true }, onSaved: vi.fn() };
  const user = userEvent.setup();
  const headers = () => within(screen.getByRole("table", { name: "模型价格" }))
    .getAllByRole("columnheader").map(header => header.firstChild?.textContent);
  const openColumns = async () => {
    await user.click(screen.getByText("筛选", { selector: "summary" }));
    await user.click(screen.getByText("数据列", { selector: "summary" }));
  };
  try {
    const view = render(<PriceSettings {...props} />);
    await openColumns();
    await user.click(screen.getByRole("checkbox", { name: "输出" }));
    await user.click(screen.getByRole("checkbox", { name: /缓存写入.*5 分钟/ }));
    await user.click(screen.getByRole("checkbox", { name: /缓存写入.*1 小时/ }));
    await user.click(screen.getByRole("checkbox", { name: "售卖比例" }));
    const expected = ["模型", "输入", "缓存读取", "Fast", "Ultrafast", "缓存写入 · 默认 / 5 分钟", "缓存写入 · 长时 / 1 小时", "售卖比例"];
    expect(headers()).toEqual(expected);
    const claudeRow = screen.getByRole("button", { name: "编辑 claude-opus-5-5" }).closest("tr")!;
    expect(within(claudeRow).getAllByRole("cell").slice(4).map(cell => cell.textContent))
      .toEqual(["$5", "$8", "15%"]);
    view.unmount();
    render(<PriceSettings {...props} />);
    expect(headers()).toEqual(expected);
    await openColumns();
    for (const checkbox of screen.getAllByRole("checkbox", { checked: true })) await user.click(checkbox);
    expect(headers()).toEqual(["模型"]);
    expect(screen.getByRole("button", { name: "编辑 claude-opus-5-5" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "恢复默认列" }));
    expect(headers()).toEqual(["模型", "输入", "输出", "缓存读取", "Fast", "Ultrafast"]);
  } finally {
    localStorage.removeItem(key);
  }
});

it("saves the grouped price fields including independent TTL rates and preserves derived cache rates", async () => {
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
  openEditor("claude-opus-5-5");
  const editor = within(screen.getByRole("dialog", { name: "claude-opus-5-5" }));
  expect(editor.getByRole("group", { name: /基础单价/ })).toBeVisible();
  expect(editor.getByRole("group", { name: /缓存单价/ })).toBeVisible();
  expect(editor.getByRole("group", { name: "估算规则" })).toBeVisible();
  expect(editor.getAllByRole("heading", { name: "claude-opus-5-5" })).toHaveLength(1);
  for (const [label, value] of [["输入", "4.5"], ["输出", "21"], ["缓存读取", "0.35"],
    ["缓存写入 · 5 分钟", "6.25"], ["缓存写入 · 1 小时", "9.5"]]) {
    await edit(`claude-opus-5-5 ${label}价格`, value);
  }
  await user.click(screen.getByLabelText("claude-opus-5-5 确认价格并用于估算"));
  const claude = screen.getByLabelText("claude-opus-5-5 输入价格").closest("form")!;
  expect(claude).toHaveTextContent("未保存");
  await user.click(within(claude).getByRole("button", { name: "保存价格" }));
  await waitFor(() => expect(saved[0]).toMatchObject({ model: "claude-opus-5-5", input: 4.5, output: 21, cache_read: .35, cache_write: 6.25, cache_write_1h: 9.5, verified: false }));
  expect(claude).not.toHaveTextContent("未保存");
  openEditor("grok-4.7");
  await edit("grok-4.7 输入价格", "3.25");
  const grok = screen.getByLabelText("grok-4.7 输入价格").closest("form")!;
  expect(within(grok).getByText(/缓存写入随输入单价/)).toBeVisible();
  await user.click(within(grok).getByRole("button", { name: "保存价格" }));
  await waitFor(() => expect(saved[1]).toMatchObject({ model: "grok-4.7", input: 3.25, cache_write: 3.25, cache_write_1h: 3.25 }));
});

it("keeps the draft and failure inside the editor without affecting other models", async () => {
  let respond: (response: Response) => void = () => {};
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => { respond = resolve; })));
  render(<PriceSettings prices={[]} loading={false} connection={{ base: "", key: "", session: "test", account: true }} onSaved={vi.fn()} />);
  const user = userEvent.setup();
  openEditor("grok-4.7");
  const input = screen.getByLabelText("grok-4.7 输入价格");
  await user.clear(input); await user.type(input, "3");
  const row = input.closest("form")!;
  await user.click(within(row).getByRole("button", { name: "保存价格" }));
  expect(input).toBeDisabled();
  respond(new Response("保存暂不可用", { status: 503 }));
  expect(await screen.findByRole("alert")).toHaveTextContent("grok-4.7");
  expect(within(row).getByRole("alert")).toHaveTextContent("grok-4.7");
  expect(input).toHaveValue(3);
  expect(input).toBeEnabled();
  expect(row).toHaveTextContent("未保存");
  openEditor("gpt-6-sol");
  expect(screen.getByLabelText("gpt-6-sol 输入价格")).toBeEnabled();
});


it("uses catalog long-context defaults, persists independent toggles and can turn it off again", async () => {
  const saved: ModelPrice[] = [];
  vi.stubGlobal("fetch", vi.fn(async (_url, options) => {
    saved.push(JSON.parse(options.body));
    return Response.json({price: saved.at(-1)});
  }));
  const props = {prices: [] as ModelPrice[], loading:false, connection:{base:"",key:"",session:"test",account:true},onSaved:vi.fn()};
  const view=render(<PriceSettings {...props} />);
  for (const price of MODEL_PRICE_CATALOG.filter(p => ["gpt-6-astra","gpt-6-sol","claude-sonnet-5-5"].includes(p.model))) {
    openEditor(price.model);
    expect(screen.getByRole("checkbox",{name:`${price.model} 超过272k加价`})).toHaveProperty("checked", price.long_context_premium ?? false);
  }
  const user=userEvent.setup();
  openEditor("gpt-6-sol");
  const toggle=screen.getByRole("checkbox",{name:"gpt-6-sol 超过272k加价"});
  await user.click(toggle);
  await user.click(within(toggle.closest("form")!).getByRole("button",{name:"保存价格"}));
  await waitFor(()=>expect(props.onSaved).toHaveBeenCalledOnce());
  expect(saved[0]).toMatchObject({model:"gpt-6-sol",long_context_premium:true,input:2,output:10});
  view.unmount();
  render(<PriceSettings {...props} prices={saved} />);
  openEditor("gpt-6-sol");
  const reloaded=screen.getByRole("checkbox",{name:"gpt-6-sol 超过272k加价"});
  expect(reloaded).toBeChecked();
  await user.click(reloaded);
  await user.click(within(reloaded.closest("form")!).getByRole("button",{name:"保存价格"}));
  await waitFor(()=>expect(saved[1]).toMatchObject({long_context_premium:false}));
});

it("does not claim long-context settings were saved when the backend omits the field",async()=>{
  const onSaved=vi.fn();
  vi.stubGlobal("fetch",vi.fn(async(_url,options)=>{
    const price=JSON.parse(options.body);delete price.long_context_premium;
    return Response.json({price});
  }));
  render(<PriceSettings prices={[]} loading={false} connection={{base:"",key:"",session:"test",account:true}} onSaved={onSaved}/>);
  const user=userEvent.setup();
  openEditor("gpt-6-sol");
  const toggle=screen.getByRole("checkbox",{name:"gpt-6-sol 超过272k加价"});
  await user.click(toggle);
  await user.click(within(toggle.closest("form")!).getByRole("button",{name:"保存价格"}));
  expect(await screen.findByRole("alert")).toHaveTextContent("服务尚未确认长上下文加价设置");
  expect(onSaved).not.toHaveBeenCalled();
  expect(toggle).toBeChecked();
});

it("waits for saved prices before allowing edits instead of flashing editable defaults", () => {
  const props = {prices:[] as ModelPrice[],loading:true,connection:{base:"",key:"",session:"test",account:true},onSaved:vi.fn()};
  const view=render(<PriceSettings {...props}/>);
  expect(screen.getByRole("status",{name:"加载模型价格"})).toBeInTheDocument();
  expect(screen.queryByRole("spinbutton")).not.toBeInTheDocument();
  expect(screen.queryByText("没有匹配的检测模型。")).not.toBeInTheDocument();
  view.rerender(<PriceSettings {...props} loading={false}/>);
  expect(screen.queryByRole("status",{name:"加载模型价格"})).not.toBeInTheDocument();
  openEditor("gpt-6-astra");
  expect(screen.getByLabelText("gpt-6-astra 售卖价格百分比")).toBeEnabled();
});

it("shows both Haiku prompt tiers, updates derived prices and preserves the policy on save", async () => {
  const saved: ModelPrice[] = [];
  vi.stubGlobal("fetch", vi.fn(async (_url, options) => {
    saved.push(JSON.parse(options.body));
    return Response.json({ price: saved.at(-1) });
  }));
  const props = { prices: [] as ModelPrice[], loading: false, connection: { base: "", key: "", session: "test", account: true }, onSaved: vi.fn() };
  const view = render(<PriceSettings {...props} />);
  const user = userEvent.setup();
  await user.type(screen.getByRole("textbox", { name: "搜索模型价格" }), "claude-haiku-5-5");
  openEditor("claude-haiku-5-5");
  const row = screen.getByLabelText("claude-haiku-5-5 输入价格").closest("form")!;
  expect(row).toHaveTextContent("自动分档");
  expect(screen.queryByRole("checkbox", { name: "claude-haiku-5-5 超过272k加价" })).not.toBeInTheDocument();
  expect(screen.getByLabelText("claude-haiku-5-5 输入价格")).toHaveValue(.1);
  expect(screen.getByLabelText("claude-haiku-5-5 输出价格")).toHaveValue(.5);
  for (const [field, value] of [["输入", "$0.5"], ["输出", "$2.5"], ["缓存读取", "$0.05"], ["缓存写入 · 5 分钟", "$0.625"], ["缓存写入 · 1 小时", "$1"]]) {
    expect(screen.getByLabelText(`claude-haiku-5-5 超过100k ${field}价格`)).toHaveTextContent(value);
  }
  await user.clear(screen.getByLabelText("claude-haiku-5-5 输入价格"));
  await user.type(screen.getByLabelText("claude-haiku-5-5 输入价格"), "0.2");
  expect(screen.getByLabelText("claude-haiku-5-5 超过100k 输入价格")).toHaveTextContent("$1");
  await user.click(within(row).getByRole("button", { name: "保存价格" }));
  await waitFor(() => expect(props.onSaved).toHaveBeenCalledOnce());
  expect(saved[0]).toMatchObject({ model: "claude-haiku-5-5", input: .2, cache_read: .01, cache_write: .125, cache_write_1h: .2, long_context_premium: false });
  view.unmount();
  render(<PriceSettings {...props} prices={saved} />);
  openEditor("claude-haiku-5-5");
  expect(screen.getByLabelText("claude-haiku-5-5 输入价格")).toHaveValue(.2);
  expect(screen.getByLabelText("claude-haiku-5-5 超过100k 输入价格")).toHaveTextContent("$1");
  await user.click(screen.getByRole("button", { name: "查看 claude-haiku-5-5 价格说明" }));
  const dialog = screen.getByRole("dialog", { name: "claude-haiku-5-5" });
  expect(dialog).toHaveTextContent("总输入（含缓存）≤100,000");
  expect(dialog).toHaveTextContent("不叠加 272k 加价");
});

it("does not claim the automatic Haiku tier works against a backend that has not been upgraded", async () => {
  const onSaved = vi.fn();
  vi.stubGlobal("fetch", vi.fn(async (_url, options) => {
    const price = JSON.parse(options.body);
    delete price.prompt_price_tier;
    return Response.json({ price });
  }));
  render(<PriceSettings prices={[]} loading={false} connection={{ base: "", key: "", session: "test", account: true }} onSaved={onSaved} />);
  openEditor("claude-haiku-5-5");
  const row = screen.getByLabelText("claude-haiku-5-5 输入价格").closest("form")!;
  await userEvent.setup().click(within(row).getByRole("button", { name: "采用参考价" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("服务尚未确认 100k 分档规则");
  expect(onSaved).not.toHaveBeenCalled();
});

it("preserves a draft across searches and renders confirmed prices read-only on a failed fetch", () => {
  const props = { prices: [] as ModelPrice[], loading: false, connection: { base: "", key: "", session: "test", account: true }, onSaved: vi.fn() };
  const view = render(<PriceSettings {...props} />, {wrapper:Tooltip.Provider});
  const search = screen.getByRole("textbox", { name: "搜索模型价格" });
  openEditor("gpt-6-astra");
  const input = screen.getByLabelText("gpt-6-astra 输入价格");
  fireEvent.change(input, { target: { value: "987.65" } });
  closeEditor();
  fireEvent.change(search, { target: { value: "no-such-model" } });
  expect(input).not.toBeInTheDocument();
  expect(screen.getByText(/1 项未保存/)).toBeVisible();
  fireEvent.change(search, { target: { value: "" } });
  openEditor("gpt-6-astra");
  const restored = screen.getByLabelText("gpt-6-astra 输入价格");
  expect(restored).toHaveValue(987.65);
  view.rerender(<PriceSettings {...props} error="读取失败" />);
  expect(restored).toBeDisabled();
  expect(screen.getByText(/参考价只读/)).toBeInTheDocument();
  expect(within(restored.closest("form")!).getByRole("button", { name: "保存价格" })).toBeDisabled();
});
import * as Tooltip from "@radix-ui/react-tooltip";

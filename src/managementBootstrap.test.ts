import { afterEach, expect, it, vi } from "vitest";
import { startManagementBootstrap, startManagementHintRead } from "./managementBootstrap";
import { readManagementPage } from "./managementQuery";
import { rememberManagementOwner, managementOwnerHint } from "./accountSessionHint";
import { loadSubFilters, saveSubFilters } from "./sub2apiPreferences";
import { saveView } from "./preferences";

afterEach(() => { globalThis.__uniManagementBootstrap?.controller.abort(); delete globalThis.__uniManagementBootstrap; localStorage.clear(); sessionStorage.clear(); vi.unstubAllGlobals(); });

it.each([null, { enabled: true, authenticated: false, username: "alice" }, { enabled: false, authenticated: true, username: "alice" }])("does not read private rows without authenticated management navigation", session => {
  const fetcher = vi.fn(); vi.stubGlobal("fetch", fetcher);
  saveView(location.origin, "sub2api"); startManagementBootstrap(session);
  expect(fetcher).not.toHaveBeenCalled();
});

it("only starts the active management page, and cancels an older initial read", () => {
  vi.stubGlobal("fetch", vi.fn((_url, init) => new Promise((_resolve, reject) => init.signal.addEventListener("abort", () => reject(init.signal.reason), { once: true }))));
  const session = { enabled: true, authenticated: true, username: "alice" };
  saveView(location.origin, "overview"); startManagementBootstrap(session); expect(fetch).not.toHaveBeenCalled();
  saveView(location.origin, "sub2api"); startManagementBootstrap(session);
  const initial = globalThis.__uniManagementBootstrap!;
  expect(fetch).toHaveBeenCalledTimes(1); expect(initial.owner).toBe("alice");
  startManagementBootstrap(session); expect(initial.controller.signal.aborted).toBe(false); expect(fetch).toHaveBeenCalledTimes(1);
  saveSubFilters("alice", {...loadSubFilters("alice"), model:"gpt-5.5"});
  startManagementBootstrap(session); expect(initial.controller.signal.aborted).toBe(true); expect(fetch).toHaveBeenCalledTimes(2);
});

it("consumes the same live request once and retains caller cancellation", async () => {
  const filters = loadSubFilters("alice");
  const controller = new AbortController();
  const result = Promise.reject(Error("current read failed")); void result.catch(() => undefined);
  globalThis.__uniManagementBootstrap = { owner: "alice", filters: JSON.stringify(filters), controller, startedAt: Date.now(), result };
  const fetcher = vi.fn(); vi.stubGlobal("fetch", fetcher);
  await expect(readManagementPage(filters, "", new AbortController().signal, "alice")).rejects.toThrow("current read failed");
  expect(fetcher).not.toHaveBeenCalled(); expect(globalThis.__uniManagementBootstrap).toBeUndefined();
});

it("discards a different owner or filter instead of displaying that result", async () => {
  const controller = new AbortController();
  globalThis.__uniManagementBootstrap = { owner: "other", filters: "{}", controller, startedAt: Date.now(), result: Promise.resolve({} as never) };
  vi.stubGlobal("fetch", vi.fn(async () => new Response("denied", { status: 401 })));
  await expect(readManagementPage(loadSubFilters("alice"), "", new AbortController().signal, "alice")).rejects.toThrow();
  expect(controller.signal.aborted).toBe(true); expect(fetch).toHaveBeenCalledTimes(1);
});

it("cancels the initial request when the mounted page changes filters", async () => {
  const filters = loadSubFilters("alice"), controller = new AbortController(), caller = new AbortController();
  const result = new Promise<never>((_resolve, reject) => controller.signal.addEventListener("abort", () => reject(controller.signal.reason), { once: true }));
  globalThis.__uniManagementBootstrap = { owner: "alice", filters: JSON.stringify(filters), controller, startedAt: Date.now(), result };
  const request = readManagementPage(filters, "", caller.signal, "alice");
  caller.abort(new DOMException("filter changed", "AbortError"));
  await expect(request).rejects.toMatchObject({ name: "AbortError" });
  expect(controller.signal.aborted).toBe(true);
});

it("validates the complete page contract even for an early request", async () => {
  const filters = loadSubFilters("alice");
  globalThis.__uniManagementBootstrap = { owner: "alice", filters: JSON.stringify(filters), controller: new AbortController(), startedAt: Date.now(), result: Promise.resolve({} as never) };
  await expect(readManagementPage(filters, "", new AbortController().signal, "alice")).rejects.toThrow("渠道分页数据不完整");
});


it("overlaps a fresh cookie-authorized read with auth without trusting the owner hint", () => {
  vi.stubGlobal("fetch", vi.fn((_url, init) => new Promise((_resolve, reject) => init.signal.addEventListener("abort", () => reject(init.signal.reason), { once: true }))));
  saveView(location.origin, "sub2api");
  rememberManagementOwner({ enabled:true, authenticated:true, username:"alice" });
  startManagementHintRead();
  const pending = globalThis.__uniManagementBootstrap!;
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(pending.owner).toBe("alice");
  startManagementBootstrap({ enabled:true, authenticated:true, username:"alice" });
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(globalThis.__uniManagementBootstrap).toBe(pending);
  startManagementBootstrap({ enabled:true, authenticated:false, username:"" });
  expect(pending.controller.signal.aborted).toBe(true);
  expect(globalThis.__uniManagementBootstrap).toBeUndefined();
  expect(managementOwnerHint()).toBe("");
});

it("replaces a previous user hint with a live authenticated different user", () => {
  vi.stubGlobal("fetch", vi.fn((_url, init) => new Promise((_resolve, reject) => init.signal.addEventListener("abort", () => reject(init.signal.reason), { once: true }))));
  saveView(location.origin, "sub2api");
  rememberManagementOwner({ enabled:true, authenticated:true, username:"alice" });
  startManagementHintRead();
  const previous=globalThis.__uniManagementBootstrap!;
  startManagementBootstrap({ enabled:true, authenticated:true, username:"bob" });
  expect(previous.controller.signal.aborted).toBe(true);
  expect(globalThis.__uniManagementBootstrap?.owner).toBe("bob");
  expect(managementOwnerHint()).toBe("bob");
});

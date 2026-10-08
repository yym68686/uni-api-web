import { controlRequest } from "./api";
export type AccountSession = {
  enabled: boolean;
  authenticated: boolean;
  username: string;
};
declare global {
  var __uniConsoleSessionBootstrap: { startedAt: number; result: Promise<unknown> } | undefined;
}
export async function readAccountSession(): Promise<AccountSession> {
  const bootstrap = globalThis.__uniConsoleSessionBootstrap;
  delete globalThis.__uniConsoleSessionBootstrap;
  if (bootstrap && Date.now() - bootstrap.startedAt < 10_000) {
    const value = await bootstrap.result.catch(() => null) as Partial<AccountSession> | null;
    if (value && typeof value.enabled === "boolean" && typeof value.authenticated === "boolean" && typeof value.username === "string") return value as AccountSession;
  }
  return controlRequest<AccountSession>("/v1/auth/me");
}

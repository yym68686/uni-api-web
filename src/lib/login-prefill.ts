export const LOGIN_PREFILL_SESSION_KEY = "uai_login_prefill";

interface LoginPrefillPayload {
  email: string;
  password: string;
  createdAt: number;
}

function isLoginPrefillPayload(value: unknown): value is LoginPrefillPayload {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.email === "string" &&
    typeof v.password === "string" &&
    typeof v.createdAt === "number" &&
    Number.isFinite(v.createdAt)
  );
}

export function writeLoginPrefill(values: { email: string; password: string }) {
  if (typeof window === "undefined") return;
  try {
    const payload: LoginPrefillPayload = {
      email: values.email,
      password: values.password,
      createdAt: Date.now()
    };
    window.sessionStorage.setItem(LOGIN_PREFILL_SESSION_KEY, JSON.stringify(payload));
  } catch {
    // ignore
  }
}

export function readLoginPrefill(options?: { maxAgeMs?: number }) {
  if (typeof window === "undefined") return null;
  const maxAgeMs = options?.maxAgeMs ?? 10 * 60 * 1000;

  try {
    const raw = window.sessionStorage.getItem(LOGIN_PREFILL_SESSION_KEY);
    if (!raw) return null;

    window.sessionStorage.removeItem(LOGIN_PREFILL_SESSION_KEY);
    const json: unknown = JSON.parse(raw);
    if (!isLoginPrefillPayload(json)) return null;

    if (Date.now() - json.createdAt > maxAgeMs) return null;
    return { email: json.email, password: json.password };
  } catch {
    try {
      window.sessionStorage.removeItem(LOGIN_PREFILL_SESSION_KEY);
    } catch {
      // ignore
    }
    return null;
  }
}


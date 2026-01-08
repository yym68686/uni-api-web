export const SESSION_COOKIE_NAME = "uai_session";

export function isLoggedInCookie(value: string | undefined) {
  return typeof value === "string" && value.length >= 16;
}


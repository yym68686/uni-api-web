import "server-only";

export function getAppName() {
  const name = process.env.APP_NAME?.trim();
  return name && name.length > 0 ? name : "MyApp";
}


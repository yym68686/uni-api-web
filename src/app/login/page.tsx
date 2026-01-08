import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { BrandPanel } from "@/components/auth/brand-panel";
import { LoginForm } from "@/components/auth/login-form";
import { SESSION_COOKIE_NAME } from "@/lib/auth";

interface LoginPageProps {
  searchParams?: Promise<Record<string, string | string[] | undefined>>;
}

export default async function LoginPage({ searchParams }: LoginPageProps) {
  const token = (await cookies()).get(SESSION_COOKIE_NAME)?.value;
  if (token) redirect("/dashboard");

  const params = (await searchParams) ?? {};
  const next =
    typeof params.next === "string"
      ? params.next
      : Array.isArray(params.next)
        ? params.next[0]
        : undefined;

  return (
    <div className="min-h-dvh bg-background">
      <div className="grid min-h-dvh grid-cols-1 lg:grid-cols-2">
        <BrandPanel />
        <section className="flex items-center justify-center px-6 py-12 lg:px-12">
          <LoginForm nextPath={next} />
        </section>
      </div>
    </div>
  );
}

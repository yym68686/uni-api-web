import { Shield, User } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { buildBackendUrl, getBackendAuthHeaders } from "@/lib/backend";

export const dynamic = "force-dynamic";

interface MeResponse {
  id: string;
  email: string;
  role: string;
  group: string;
  balance: number;
  orgId: string;
  createdAt: string;
  lastLoginAt: string | null;
}

function isMeResponse(value: unknown): value is MeResponse {
  if (!value || typeof value !== "object") return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.id === "string" &&
    typeof v.email === "string" &&
    typeof v.role === "string" &&
    typeof v.group === "string" &&
    typeof v.balance === "number" &&
    typeof v.orgId === "string" &&
    typeof v.createdAt === "string" &&
    (v.lastLoginAt === null || typeof v.lastLoginAt === "string")
  );
}

function formatUtcDateTime(value: string | null) {
  if (!value) return "—";
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return "—";
  return dt.toISOString().replace("T", " ").slice(0, 16);
}

function roleVariant(role: string): "outline" | "success" | "warning" {
  if (role === "owner") return "warning";
  if (role === "admin") return "success";
  return "outline";
}

async function getMe() {
  const res = await fetch(buildBackendUrl("/auth/me"), {
    cache: "no-store",
    headers: await getBackendAuthHeaders()
  });
  if (!res.ok) return null;
  const json: unknown = await res.json();
  if (!isMeResponse(json)) return null;
  return json;
}

export default async function ProfilePage() {
  const me = await getMe();

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Profile</h1>
        <p className="mt-1 text-sm text-muted-foreground">你的账号信息与当前 Workspace。</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <User className="h-5 w-5 text-muted-foreground" />
            Account
          </CardTitle>
          <CardDescription>用于快速确认登录身份、角色、用户组与余额。</CardDescription>
        </CardHeader>
        <CardContent>
          {!me ? (
            <div className="rounded-xl border border-dashed border-border bg-muted/10 p-8 text-center text-sm text-muted-foreground">
              无法获取账号信息
            </div>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">Email</div>
                <div className="mt-1 truncate text-sm font-medium text-foreground">{me.email}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">Role</div>
                <div className="mt-1 flex items-center gap-2">
                  <Badge variant={roleVariant(me.role)}>{me.role}</Badge>
                  <Shield className="h-4 w-4 text-muted-foreground" />
                </div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">Group</div>
                <div className="mt-1 font-mono text-xs text-foreground">{me.group}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">Balance</div>
                <div className="mt-1 font-mono text-sm text-foreground">{me.balance}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">Created</div>
                <div className="mt-1 font-mono text-xs text-foreground">{formatUtcDateTime(me.createdAt)}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4">
                <div className="text-xs text-muted-foreground">Last login</div>
                <div className="mt-1 font-mono text-xs text-foreground">{formatUtcDateTime(me.lastLoginAt)}</div>
              </div>
              <div className="rounded-xl border border-border bg-background/35 p-4 sm:col-span-2">
                <div className="text-xs text-muted-foreground">Workspace</div>
                <div className="mt-1 font-mono text-xs text-foreground">{me.orgId}</div>
              </div>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}


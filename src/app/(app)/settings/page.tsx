import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export default function SettingsPage() {
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          预留：组织、计费、限流、Webhooks、模型路由等。
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Coming soon</CardTitle>
          <CardDescription>你想先做哪块？我可以继续补齐。</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground">
          - Usage filters / 时间范围
          <br />- 细粒度 scopes / 权限策略
          <br />- 成本预算与告警
        </CardContent>
      </Card>
    </div>
  );
}


import type * as React from "react";
import { ShieldAlert } from "lucide-react";

import { EmptyState } from "@/components/common/empty-state";
import { Card, CardContent } from "@/components/ui/card";

interface AdminForbiddenCardProps {
  title: React.ReactNode;
  description?: React.ReactNode;
}

export function AdminForbiddenCard({ title, description }: AdminForbiddenCardProps) {
  return (
    <Card>
      <CardContent className="p-6">
        <EmptyState
          icon={<ShieldAlert className="h-6 w-6 text-muted-foreground uai-float-sm" />}
          title={title}
          description={description}
        />
      </CardContent>
    </Card>
  );
}

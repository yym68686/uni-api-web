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
          icon={(
            <span className="inline-flex uai-float-sm">
              <ShieldAlert className="h-6 w-6 text-muted-foreground" />
            </span>
          )}
          title={title}
          description={description}
        />
      </CardContent>
    </Card>
  );
}

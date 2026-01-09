import { Suspense } from "react";

import { KeysContent } from "./_components/keys-content";
import { KeysPageSkeleton } from "./_components/keys-skeleton";

export const dynamic = "force-dynamic";

export default async function ApiKeysPage() {
  return (
    <Suspense fallback={<KeysPageSkeleton />}>
      <KeysContent />
    </Suspense>
  );
}

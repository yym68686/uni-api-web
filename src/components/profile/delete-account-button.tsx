"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/i18n/i18n-provider";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface DeleteAccountButtonProps {
  className?: string;
}

function readMessage(json: unknown, fallback: string) {
  if (!json || typeof json !== "object") return fallback;
  if ("message" in json && typeof (json as { message?: unknown }).message === "string") {
    const message = (json as { message?: string }).message ?? "";
    if (message) return message;
  }
  return fallback;
}

export function DeleteAccountButton({ className }: DeleteAccountButtonProps) {
  const router = useRouter();
  const { t } = useI18n();
  const [open, setOpen] = React.useState(false);
  const [confirmText, setConfirmText] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);

  const canSubmit = confirmText.trim().toUpperCase() === "DELETE";

  async function handleDelete() {
    if (!canSubmit || submitting) return;
    setSubmitting(true);
    try {
      const res = await fetch("/api/account", { method: "DELETE" });
      const json: unknown = await res.json().catch(() => null);
      if (!res.ok) throw new Error(readMessage(json, t("profile.delete.failed")));

      await fetch("/api/auth/logout", { method: "POST" }).catch(() => null);
      toast.success(t("profile.delete.success"));
      setOpen(false);
      router.replace("/register");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("profile.delete.failed"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <Button
        type="button"
        variant="destructive"
        className={className}
        onClick={() => {
          setConfirmText("");
          setOpen(true);
        }}
      >
        {t("profile.delete.button")}
      </Button>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!submitting) setOpen(next);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="h-5 w-5 text-destructive" />
              {t("profile.delete.title")}
            </DialogTitle>
            <DialogDescription>
              {t("profile.delete.desc")}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-2">
            <Label htmlFor="confirm-delete" className="text-sm">
              {t("profile.delete.confirmLabel")}
            </Label>
            <Input
              id="confirm-delete"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              placeholder={t("profile.delete.placeholder")}
              autoComplete="off"
            />
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              disabled={submitting}
              onClick={() => setOpen(false)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={!canSubmit || submitting}
              onClick={() => void handleDelete()}
            >
              {submitting ? t("profile.delete.submitting") : t("profile.delete.button")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

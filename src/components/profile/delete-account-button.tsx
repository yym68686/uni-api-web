"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
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
      if (!res.ok) throw new Error(readMessage(json, "注销失败"));

      await fetch("/api/auth/logout", { method: "POST" }).catch(() => null);
      toast.success("账号已注销");
      setOpen(false);
      router.replace("/register");
      router.refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "注销失败");
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
        注销账号
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
              注销账号（不可恢复）
            </DialogTitle>
            <DialogDescription>
              将永久删除你的账号、会话和 API Keys。输入 <span className="font-mono">DELETE</span>{" "}
              以确认。
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-2">
            <Label htmlFor="confirm-delete" className="text-sm">
              Confirm
            </Label>
            <Input
              id="confirm-delete"
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              placeholder="Type DELETE"
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
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={!canSubmit || submitting}
              onClick={() => void handleDelete()}
            >
              {submitting ? "Deleting…" : "Delete account"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}


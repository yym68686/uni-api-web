import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { Plus, X } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, controlRequest } from "./api";
import type { ConsoleSource } from "./SourceSettings";
import type { KeyInfo } from "./types";

const MODELS = ["jev-latest", "jev-preview", "jev-1.13.0"];
interface Result {
  status: string;
  operation_id: string;
  message?: string;
}

export function TypeSafeChannel({
  sources,
  keys,
  sourceId,
  keyId,
}: {
  sources: ConsoleSource[];
  keys: KeyInfo[];
  sourceId: string;
  keyId: string;
}) {
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const [source, setSource] = useState("");
  const [key, setKey] = useState("");
  const [name, setName] = useState("jev");
  const [secret, setSecret] = useState("");
  const [models, setModels] = useState([...MODELS]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState<{ source: string; id: string } | null>(
    null,
  );
  const available = keys.filter(
    (k) => k.source_id === source || (!k.source_id && sources.length === 1),
  );
  const rawKey = key.includes("::") ? key.split("::").slice(1).join("::") : key;
  const path = `/v1/sources/${encodeURIComponent(source)}/channel-settings`;

  function finish(result: Result) {
    if (result.status === "applied") {
      setPending(null);
      setSecret("");
      setMessage("Jev 渠道已添加，配置已保存。");
      for (const queryKey of [
        "catalog",
        "keys",
        "channel-controls",
        "metrics",
        "imported-channels",
      ]) {
        void cache.invalidateQueries({ queryKey: [queryKey] });
      }
    } else if (result.status === "rejected") {
      setPending(null);
      setMessage(result.message || "配置未应用，请核对后重试。");
    } else {
      setMessage("保存结果待确认，请点击“核对保存结果”。");
    }
  }

  async function save() {
    if (busy) return;
    setBusy(true);
    setMessage("");
    try {
      if (pending) {
        finish(
          await controlRequest<Result>(
            `/v1/sources/${encodeURIComponent(pending.source)}/channel-settings/operations/${encodeURIComponent(pending.id)}`,
          ),
        );
        return;
      }
      if (!available.some((k) => k.key_id === key))
        throw new Error("请选择当前来源下的 API key。");
      const schema = await controlRequest<{ create_typesafe?: boolean }>(
        `${path}/schema`,
      );
      if (!schema.create_typesafe)
        throw new Error("该来源尚不支持添加 Jev，请先更新 uni-api。");
      const controls = await controlRequest<{ revision: string }>(
        `/v1/sources/${encodeURIComponent(source)}/channel-controls`,
      );
      const mutation = {
        revision: controls.revision,
        operation_id: crypto.randomUUID(),
        changes: [
          {
            provider: `typesafe-${name}`,
            create_to_key: rawKey,
            set: {
              "/engine": "typesafe",
              "/base_url": "https://api.typesafe.ai/v1/systemone",
              "/api": secret.trim(),
              "/model": models,
              "/preferences/balance_query": false,
            },
            remove: [],
          },
        ],
        sample: {
          model: models[0],
          endpoint: "/v1/systemone",
          stream: false,
          body: {
            state: "Service is healthy.",
            questions: {
              healthy: {
                type: "noul",
                instructions: "Is the service healthy?",
              },
            },
          },
        },
      };
      await controlRequest(`${path}/validate`, {
        method: "POST",
        body: JSON.stringify(mutation),
      });
      // After dispatch, keep the operation ID until persistence is confirmed;
      // a lost response must not create a second channel or discard the result.
      setPending({ source, id: mutation.operation_id });
      finish(
        await controlRequest<Result>(path, {
          method: "PATCH",
          body: JSON.stringify(mutation),
        }),
      );
    } catch (error) {
      if (
        error instanceof ApiError &&
        [400, 401, 403, 404, 409].includes(error.status)
      )
        setPending(null);
      setMessage(error instanceof Error ? error.message : "添加失败");
    } finally {
      setBusy(false);
    }
  }

  function changeOpen(next: boolean) {
    if (busy) return;
    setOpen(next);
    if (!next) {
      setSecret("");
      return;
    }
    if (!pending) {
      const initial = sourceId || keyId.split("::")[0];
      setSource(
        sources.some((s) => s.id === initial) ? initial : sources[0]?.id || "",
      );
      setKey(keyId);
      setMessage("");
    }
  }

  return (
    <Dialog.Root open={open} onOpenChange={changeOpen}>
      <Dialog.Trigger asChild>
        <button className="button small ghost">
          <Plus size={15} />
          添加 Jev 渠道
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay" />
        <Dialog.Content className="typesafe-dialog">
          <Dialog.Title>添加 TypeSafe / Jev 渠道</Dialog.Title>
          <Dialog.Description className="settings-note">
            输入 $0.042 / 百万 tokens，输出免费。渠道仅供所选 API key 使用。
          </Dialog.Description>
          <fieldset
            disabled={busy || !!pending}
            style={{ border: 0, padding: 0, display: "grid", gap: 14 }}
          >
            <label>
              uni-api 来源
              <select
                aria-label="Jev 来源"
                value={source}
                onChange={(e) => {
                  setSource(e.target.value);
                  setKey("");
                }}
              >
                {sources.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              调用 API key
              <select
                aria-label="Jev 调用 API key"
                value={key}
                onChange={(e) => setKey(e.target.value)}
              >
                <option value="">选择 API key</option>
                {available.map((k) => (
                  <option key={k.key_id} value={k.key_id}>
                    #{k.position} · {k.prefix}
                  </option>
                ))}
              </select>
            </label>
            <label>
              渠道名称
              <input
                aria-label="Jev 渠道名称"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="jev"
              />
            </label>
            <label>
              TypeSafe API key
              <input
                aria-label="TypeSafe API key"
                type="password"
                autoComplete="off"
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
              />
            </label>
            <div>
              {MODELS.map((m) => (
                <label key={m} style={{ display: "block" }}>
                  <input
                    type="checkbox"
                    checked={models.includes(m)}
                    onChange={(e) =>
                      setModels((old) =>
                        e.target.checked
                          ? [...old, m]
                          : old.filter((value) => value !== m),
                      )
                    }
                  />{" "}
                  {m}
                </label>
              ))}
            </div>
          </fieldset>
          {message && (
            <p role="status" className="settings-note">
              {message}
            </p>
          )}
          <div className="dialog-footer">
            <button
              className="button"
              disabled={
                busy ||
                (!pending &&
                  (!source ||
                    !key ||
                    !secret.trim() ||
                    !models.length ||
                    !/^[a-zA-Z0-9-]{1,80}$/.test(name)))
              }
              onClick={() => void save()}
            >
              {busy ? "处理中…" : pending ? "核对保存结果" : "校验并添加"}
            </button>
            <Dialog.Close asChild>
              <button className="button ghost" disabled={busy}>
                <X size={15} />
                关闭
              </button>
            </Dialog.Close>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

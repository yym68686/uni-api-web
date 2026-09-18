import { useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { Plus, Server, Trash2, Pencil, Check, X } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import { ControlPersistence } from "./ControlPersistence";
export interface ConsoleSource {
  id: string;
  name: string;
  base: string;
  has_storage: boolean;
  created_at: number;
}
export function SourceSettings({
  sources,
  onSaved,
  refreshAction,
}: {
  sources: ConsoleSource[];
  onSaved: () => void;
  refreshAction?: ReactNode;
}) {
  const [editing, setEditing] = useState<ConsoleSource | null>(null),
    [open, setOpen] = useState(false),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const [name, setName] = useState(""),
    [base, setBase] = useState(""),
    [key, setKey] = useState("");
  const [endpoint, setEndpoint] = useState(""),
    [bucket, setBucket] = useState(""),
    [prefix, setPrefix] = useState("uni-api-facts/v1/"),
    [access, setAccess] = useState(""),
    [secret, setSecret] = useState("");
  const [removing, setRemoving] = useState<string | null>(null);
  function edit(source: ConsoleSource | null) {
    setEditing(source);
    setName(source?.name || "");
    setBase(source?.base || "");
    setKey("");
    setEndpoint("");
    setBucket("");
    setAccess("");
    setSecret("");
    setError("");
    setOpen(true);
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await controlRequest("/v1/sources" + (editing ? `/${editing.id}` : ""), {
        method: editing ? "PUT" : "POST",
        body: JSON.stringify({
          name,
          base,
          key,
          storage: bucket
            ? {
                endpoint,
                bucket,
                prefix,
                access_key: access,
                secret_key: secret,
              }
            : undefined,
        }),
      });
      setOpen(false);
      setKey("");
      setAccess("");
      setSecret("");
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }
  async function remove(id: string) {
    setBusy(true);
    try {
      await controlRequest(`/v1/sources/${id}`, { method: "DELETE" });
      setRemoving(null);
      onSaved();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="sources-settings data-panel">
      <div className="data-heading">
        <div className="data-title">
          <Server size={20} />
          <h2>uni-api 来源</h2>
        </div>
        <div className="data-actions">
          {refreshAction}
          <button className="button small" onClick={() => edit(null)}>
            <Plus size={16} />
            添加来源
          </button>
        </div>
      </div>
      <p className="settings-note">
        每个来源独立采集与统计。平台密钥和 S3
        只读凭据加密保存在服务端。默认保留已应用的临时渠道、顺序和停用规则，来源重启后自动恢复。
      </p>
      {error && (
        <p role="alert" className="error-banner">
          {error}
        </p>
      )}
      {sources.map((src) => (
        <div className="source-item" key={src.id}>
          <Server size={20} />
          <div>
            <strong>{src.name}</strong>
            <small>{src.base}</small>
            <small>
              {src.has_storage
                ? "已配置历史事实存储"
                : "仅实时目录 · 添加 S3 只读凭据以采集历史请求"}
            </small>
          </div>
          <ControlPersistence source={src.id} name={src.name} />
          <button
            className="icon-button"
            aria-label={`编辑 ${src.name}`}
            onClick={() => edit(src)}
          >
            <Pencil size={16} />
          </button>
          {removing === src.id ? (
            <>
              <button
                className="button small"
                disabled={busy}
                onClick={() => void remove(src.id)}
              >
                确认移除
              </button>
              <button
                className="icon-button"
                onClick={() => setRemoving(null)}
                aria-label="取消移除"
              >
                <X size={16} />
              </button>
            </>
          ) : (
            <button
              className="icon-button"
              aria-label={`移除 ${src.name}`}
              onClick={() => setRemoving(src.id)}
            >
              <Trash2 size={16} />
            </button>
          )}
        </div>
      ))}
      {!sources.length && !open && (
        <p className="settings-note">
          还没有来源。添加第一个 uni-api 后即可开始观测。
        </p>
      )}
      {open && (
        <form className="source-form" onSubmit={save}>
          <h3>{editing ? "编辑来源" : "添加来源"}</h3>
          <label>
            来源名称
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
              maxLength={100}
            />
          </label>
          <label>
            uni-api 地址
            <input
              type="url"
              placeholder="https://api.example.com"
              value={base}
              onChange={(e) => setBase(e.target.value)}
              required
            />
          </label>
          <label>
            平台密钥
            <input
              type="password"
              autoComplete="new-password"
              placeholder={editing ? "留空保留原密钥" : "配置中的第一个密钥"}
              value={key}
              onChange={(e) => setKey(e.target.value)}
              required={!editing}
            />
          </label>
          <details>
            <summary>
              S3 历史事实存储 {editing ? "（留空保留原配置）" : ""}
            </summary>
            <p>
              填写 uni-api 正在写入的独立 bucket/prefix
              和只读凭据。未配置时历史统计显示为未接入，不会显示为零流量。
            </p>
            <div className="source-storage-fields">
              <label>
                S3 Endpoint
                <input
                  type="url"
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                  required={!!bucket}
                />
              </label>
              <label>
                Bucket
                <input
                  value={bucket}
                  onChange={(e) => setBucket(e.target.value)}
                />
              </label>
              <label>
                Prefix
                <input
                  value={prefix}
                  onChange={(e) => setPrefix(e.target.value)}
                />
              </label>
              <label>
                Access key
                <input
                  autoComplete="off"
                  value={access}
                  onChange={(e) => setAccess(e.target.value)}
                  required={!!bucket}
                />
              </label>
              <label>
                Secret key
                <input
                  type="password"
                  autoComplete="new-password"
                  value={secret}
                  onChange={(e) => setSecret(e.target.value)}
                  required={!!bucket}
                />
              </label>
            </div>
          </details>
          <button className="button primary" disabled={busy}>
            {busy ? <Spinner small /> : <Check size={16} />}验证并保存
          </button>
          <button
            className="button"
            type="button"
            onClick={() => setOpen(false)}
          >
            取消
          </button>
        </form>
      )}
      <p className="settings-note">
        移除来源会停止展示和导入；已经存储在 S3
        的事实不会删除。不同来源若共用上游账号，上游账单会包含共享用量，不能逐行相加。
      </p>
      <PasswordSettings />
    </section>
  );
}

function PasswordSettings() {
  const [current, setCurrent] = useState(""),
    [password, setPassword] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await controlRequest("/v1/auth/password", {
        method: "PUT",
        body: JSON.stringify({ current, password }),
      });
      location.reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : "修改失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="source-form">
      <summary>修改登录密码</summary>
      <form onSubmit={submit}>
        <label>
          当前密码
          <input
            type="password"
            autoComplete="current-password"
            required
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </label>
        <label>
          新密码（至少 12 个字符）
          <input
            type="password"
            autoComplete="new-password"
            minLength={12}
            maxLength={72}
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error && <p role="alert">{error}</p>}
        <p>保存后退出所有会话，使用新密码重新登录。</p>
        <button className="button" disabled={busy}>
          修改密码
        </button>
      </form>
    </details>
  );
}

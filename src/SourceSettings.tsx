import { SourceLoading } from "./PageLoading";
import { useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import type { FormEvent, ReactNode } from "react";
import { Plus, Server, Trash2, Pencil, Check } from "lucide-react";
import { controlRequest } from "./api";
import { Spinner } from "./ui";
import { ChannelSettings, GLOBAL_SETTINGS_SCOPE } from "./ChannelSettings";
import { ControlPersistence } from "./ControlPersistence";
import { ManagedConfiguration } from "./ManagedConfiguration";
import { ChannelProtocolRepair } from "./ChannelProtocolRepair";
import { FingerprintSettings } from "./FingerprintSettings";
import { WorkbenchDisclosure, WorkbenchNotice, WorkbenchTabs } from "./Workbench";
import { CompactDialogHeader, RemovalDialog } from "./CompactDialog";
export interface ConsoleSource {
  id: string;
  name: string;
  base: string;
  has_storage: boolean;
  has_config_key?: boolean;
  temporarily_unavailable?: boolean;
  created_at: number;
}
export function SourceSettings({
  sources,
  onSaved,
  refreshAction,
  loading = false,
  readError,
}: {
  sources: ConsoleSource[];
  onSaved: () => void;
  refreshAction?: ReactNode;
  loading?: boolean;
  readError?: string;
}) {
  const [editing, setEditing] = useState<ConsoleSource | null>(null),
    [open, setOpen] = useState(false),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const [name, setName] = useState(""),
    [base, setBase] = useState(""),
    [key, setKey] = useState(""),
    [configKey, setConfigKey] = useState("");
  const [removing, setRemoving] = useState<string | null>(null);
  const [managedBootstrap, setManagedBootstrap] = useState(false);
  const [section, setSection] = useState("sources");
  const opener = useRef<HTMLElement | null>(null);
  function edit(source: ConsoleSource | null) {
    opener.current = document.activeElement as HTMLElement;
    setEditing(source);
    setName(source?.name || "");
    setBase(source?.base || "");
    setKey("");
    setConfigKey("");
    setManagedBootstrap(false);
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
          config_key: configKey,
          managed_bootstrap: managedBootstrap,
        }),
      });
      setOpen(false);
      setKey("");
      setConfigKey("");
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }
  async function remove(id: string) {
    if (busy) return;
    setError("");
    setBusy(true);
    try {
      await controlRequest(`/v1/sources/${encodeURIComponent(id)}`, { method: "DELETE" });
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
          <h2>设置</h2>
          {section === "sources" && <WorkbenchNotice urgent label="来源设置状态" messages={[!open && !removing && error, readError && `来源目录读取失败，现有来源未改变：${readError}`]} />}
        </div>
        {section === "sources" && <div className="data-actions">
          {refreshAction}
          <button className="button small" onClick={() => edit(null)} disabled={loading || !!readError}>
            <Plus size={16} />
            添加来源
          </button>
        </div>}
      </div>
      <WorkbenchTabs label="设置视图" value={section} onChange={setSection} tabs={[["sources", "来源"], ["fingerprints", "模型指纹"], ["account", "账户安全"]]} />
      <div hidden={section !== "sources"}>
      {loading && !sources.length && <SourceLoading />}
      {sources.map((src) => (
        <div className="source-item" key={src.id}>
          <Server size={20} />
          <div className="source-identity">
            <strong title={src.name}>{src.name}</strong>
            <small title={src.base}>{src.base}{src.temporarily_unavailable && <span role="status"> · 暂时不可用</span>}</small>
          </div>
          <WorkbenchDisclosure label="管理">
          <ChannelSettings row={{provider:GLOBAL_SETTINGS_SCOPE,provider_name:"全局设置",source_id:src.id,source_name:src.name,model:""}} />
          <ControlPersistence source={src.id} name={src.name} lazyLoad />
          <ManagedConfiguration source={src.id} />
          <ChannelProtocolRepair source={src.id} provider="" onApplied={onSaved} />
          </WorkbenchDisclosure>
          <button
            className="icon-button"
            aria-label={`编辑 ${src.name}`}
            onClick={() => edit(src)}
          >
            <Pencil size={16} />
          </button>
          <button className="icon-button" aria-label={`移除 ${src.name}`} disabled={busy}
            onClick={() => { setError(""); setRemoving(src.id); }}>
            <Trash2 size={16} />
          </button>
          {removing === src.id && <RemovalDialog title="移除来源" context={`${src.name} · ${src.base}`}
            close={() => { setRemoving(null); setError(""); }} confirm={() => void remove(src.id)} busy={busy} error={error}>
            停止接收此来源的新日志，已保存历史保留。请先核对引用此来源的自动化任务和站点绑定。
          </RemovalDialog>}
        </div>
      ))}
      {!loading && !readError && !sources.length && !open && <p className="sources-empty">暂无来源</p>}
      <Dialog.Root open={open} onOpenChange={next => {if (!busy) setOpen(next);}}>
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="connection-dialog compact-dialog" aria-describedby={undefined}
            onPointerDownOutside={event => { if (busy) event.preventDefault(); }}
            onEscapeKeyDown={event => { if (busy) event.preventDefault(); }}
            onCloseAutoFocus={event=>{event.preventDefault(); opener.current?.focus();}}>
            <CompactDialogHeader busy={busy}>{editing ? `编辑来源 · ${editing.name}` : "添加来源"}</CompactDialogHeader>
            <form className="compact-dialog-form" onSubmit={save}>
              <div className="compact-dialog-scroll">
                <fieldset className="dialog-field-grid" disabled={busy}>
                  <label className="dialog-field dialog-field-full">
                    来源名称
                    <input value={name} onChange={e => setName(e.target.value)} required maxLength={100} />
                  </label>
                  <label className="dialog-field dialog-field-full">
                    uni-api 地址
                    <input type="url" placeholder="https://api.example.com" value={base}
                      onChange={e => setBase(e.target.value)} required />
                  </label>
                  <label className="dialog-field dialog-field-full">
                    平台密钥
                    <input type="password" autoComplete="new-password"
                      placeholder={editing ? "留空保留原密钥" : managedBootstrap ? "托管配置中的管理员密钥" : "配置中的第一个密钥"}
                      value={key} onChange={e => setKey(e.target.value)} required={!editing} />
                  </label>
                </fieldset>
                <div className="compact-dialog-options">
                  {!editing && <label className="dialog-check-row">
                    <input type="checkbox" checked={managedBootstrap} disabled={busy} onChange={event => {
                      setManagedBootstrap(event.target.checked);
                      if (event.target.checked) setConfigKey("");
                    }} />
                    预登记尚未上线的托管来源
                  </label>}
                  {!managedBootstrap && <details className="dialog-inline-note">
                    <summary>渠道密钥读取 {editing?.has_config_key ? "（已配置）" : ""}</summary>
                    <label className="dialog-field">
                      配置读取管理员密钥（可选）
                      <input type="password" autoComplete="new-password"
                        placeholder={editing?.has_config_key ? "留空保留原密钥" : "平台密钥无配置读取权限时填写"}
                        value={configKey} disabled={busy} onChange={e => setConfigKey(e.target.value)} maxLength={4096} />
                    </label>
                  </details>}
                </div>
                {error && <p role="alert" className="error-banner">{error}</p>}
              </div>
              <div className="compact-dialog-footer">
                <button className="button small" type="button" disabled={busy} onClick={() => setOpen(false)}>取消</button>
                <button className="button small primary" disabled={busy}>
                  {busy ? <Spinner small /> : <Check size={16} />}{managedBootstrap ? "登记来源" : "验证并保存"}
                </button>
              </div>
            </form>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
      </div>
      {section === "fingerprints" && <FingerprintSettings />}
      {section === "account" && <PasswordSettings />}
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

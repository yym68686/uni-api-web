import { useRef, useState } from "react";
import { ArrowDown, ArrowUp, Columns3, GripVertical, RotateCcw } from "lucide-react";

export type TableColumn<K extends string> = { id: K; label: string };

export function parseTableColumns<K extends string>(value: unknown, choices: readonly TableColumn<K>[]): K[] | null {
  if (!Array.isArray(value)) return null;
  const valid = new Set(choices.map(({ id }) => id));
  return [...new Set(value.filter((id): id is K => typeof id === "string" && valid.has(id as K)))];
}

export function useTableColumns<K extends string>(key: string, choices: readonly TableColumn<K>[]) {
  const [selection, setSelection] = useState<K[] | null>(() => {
    try { return parseTableColumns(JSON.parse(localStorage.getItem(key) || "null"), choices); }
    catch { return null; }
  });
  const set = (next: K[] | null) => {
    setSelection(next);
    try {
      if (next) localStorage.setItem(key, JSON.stringify(next));
      else localStorage.removeItem(key);
    } catch { /* Column preferences also work when browser storage is disabled. */ }
  };
  return [selection, set] as const;
}

export function TableColumns<K extends string>({ choices, value, onChange, onReset, inline }: {
  choices: readonly TableColumn<K>[];
  value: readonly K[];
  onChange: (next: K[]) => void;
  onReset: () => void;
  inline?: boolean;
}) {
  const dragged = useRef<K | null>(null);
  const [drag, setDrag] = useState<{ id: K; over: K | null } | null>(null);
  const byId = new Map(choices.map(column => [column.id, column]));
  const selected = value.flatMap(id => byId.get(id) || []);
  const move = (id: K, index: number) => {
    if (index < 0 || index >= value.length) return;
    const next = value.filter(column => column !== id);
    next.splice(index, 0, id);
    onChange(next);
  };
  const fields = <>
    <ul aria-label="数据列及显示顺序" onDragLeave={event => {
      if (event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget)) return;
      setDrag(current => current?.over ? { ...current, over: null } : current);
    }}>
      {[...selected, ...choices.filter(({ id }) => !value.includes(id))].map(({ id, label }) => {
        const index = value.indexOf(id);
        const checked = index !== -1;
        const destination = drag?.over === id
          ? value.indexOf(drag.id) > index ? "before" : "after"
          : undefined;
        return <li key={id} data-column-dragging={drag?.id === id || undefined}
          data-column-drop={destination} onDragOver={event => {
          const source = dragged.current;
          if (!source) return;
          const over = checked && source !== id ? id : null;
          setDrag(current => current?.over === over ? current : { id: source, over });
          if (!checked) return;
          event.preventDefault();
          event.dataTransfer.dropEffect = "move";
        }} onDrop={event => {
          const source = dragged.current;
          if (!source || !checked) return;
          event.preventDefault();
          dragged.current = null;
          setDrag(null);
          if (source !== id) move(source, index);
        }}>
          <label><input type="checkbox" checked={checked} onChange={event => onChange(
            event.target.checked ? [...value, id] : value.filter(column => column !== id),
          )} />{label}</label>
          {checked && <div className="table-column-order">
            <span className="table-column-grip" aria-hidden="true" draggable onDragStart={event => {
              dragged.current = id;
              event.dataTransfer.effectAllowed = "move";
              event.dataTransfer.setData("text/plain", id);
              setDrag({ id, over: null });
            }} onDragEnd={() => { dragged.current = null; setDrag(null); }}><GripVertical size={14} /></span>
            <button type="button" className="icon-button" aria-label={`上移${label}列`} disabled={index === 0} onClick={() => move(id, index - 1)}><ArrowUp size={14} /></button>
            <button type="button" className="icon-button" aria-label={`下移${label}列`} disabled={index === value.length - 1} onClick={() => move(id, index + 1)}><ArrowDown size={14} /></button>
          </div>}
          {destination && drag && <span className="table-column-drop-preview" aria-hidden="true">
            {destination === "before" ? <ArrowUp size={12} /> : <ArrowDown size={12} />}
            <span>{byId.get(drag.id)?.label}</span>
          </span>}
        </li>;
      })}
    </ul>
    <div className="table-column-footer">
      <span className="muted">显示 {selected.length} / {choices.length} 列</span>
      <button className="text-link" type="button" onClick={onReset}><RotateCcw size={13} aria-hidden="true" />恢复默认列</button>
    </div>
  </>;
  return inline ? <div className="table-column-options table-column-options-inline">{fields}</div>
    : <details className="table-column-options data-disclosure"><summary><Columns3 size={14} aria-hidden="true" />数据列</summary>{fields}</details>;
}

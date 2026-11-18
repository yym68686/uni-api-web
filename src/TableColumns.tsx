import { useRef, useState } from "react";
import { ArrowDown, ArrowUp, GripVertical } from "lucide-react";

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

export function TableColumns<K extends string>({ choices, value, onChange, onReset }: {
  choices: readonly TableColumn<K>[];
  value: readonly K[];
  onChange: (next: K[]) => void;
  onReset: () => void;
}) {
  const dragged = useRef<K | null>(null);
  const byId = new Map(choices.map(column => [column.id, column]));
  const selected = value.flatMap(id => byId.get(id) || []);
  const move = (id: K, index: number) => {
    if (index < 0 || index >= value.length) return;
    const next = value.filter(column => column !== id);
    next.splice(index, 0, id);
    onChange(next);
  };
  return <details className="table-column-options">
    <summary>数据列</summary>
    <button className="text-link table-column-reset" type="button" onClick={onReset}>恢复默认列</button>
    <ul aria-label="数据列及显示顺序">
      {[...selected, ...choices.filter(({ id }) => !value.includes(id))].map(({ id, label }) => {
        const index = value.indexOf(id);
        const checked = index !== -1;
        return <li key={id} onDragOver={event => {
          if (dragged.current && checked) event.preventDefault();
        }} onDrop={event => {
          if (!dragged.current || !checked) return;
          event.preventDefault();
          if (dragged.current !== id) move(dragged.current, index);
          dragged.current = null;
        }}>
          <label><input type="checkbox" checked={checked} onChange={event => onChange(
            event.target.checked ? [...value, id] : value.filter(column => column !== id),
          )} />{label}</label>
          {checked && <div className="table-column-order">
            <span className="table-column-grip" aria-hidden="true" draggable onDragStart={event => {
              dragged.current = id;
              event.dataTransfer.effectAllowed = "move";
              event.dataTransfer.setData("text/plain", id);
            }} onDragEnd={() => { dragged.current = null; }}><GripVertical size={14} /></span>
            <button type="button" className="icon-button" aria-label={`上移${label}列`} disabled={index === 0} onClick={() => move(id, index - 1)}><ArrowUp size={14} /></button>
            <button type="button" className="icon-button" aria-label={`下移${label}列`} disabled={index === value.length - 1} onClick={() => move(id, index + 1)}><ArrowDown size={14} /></button>
          </div>}
        </li>;
      })}
    </ul>
  </details>;
}

import { useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { serviceTiers } from "./serviceTierStatus";

export const detectionProjects = [
  "quality",
  "fingerprint",
  "tool-use",
  "compaction",
  ...serviceTiers,
] as const;
export type DetectionProject = (typeof detectionProjects)[number];

export const detectionProjectLabels: Record<DetectionProject, string> = {
  quality: "降智检测",
  fingerprint: "指纹检测",
  "tool-use": "Tool use 检测",
  compaction: "压缩检测",
  fast: "Fast 检测",
  ultrafast: "Ultrafast 检测",
};

export function useDetectionProject<T extends DetectionProject>(
  view: "observation" | "management",
  user: string,
  projects: readonly T[],
) {
  const key = `uni-console-detection-project:v1:${view}:${user}`;
  function read(): T {
    try {
      const stored = localStorage.getItem(key);
      return projects.find((project) => project === stored) ?? projects[0];
    } catch {
      return projects[0];
    }
  }
  const [selection, setSelection] = useState(() => ({ key, project: read() }));
  const project = selection.key === key ? selection.project : read();
  function select(next: T) {
    setSelection({ key, project: next });
    try {
      localStorage.setItem(key, next);
    } catch {
      /* Keep the current selection when storage is unavailable. */
    }
  }
  return [project, select] as const;
}

export function DetectionAction<T extends DetectionProject>({
  projects,
  project,
  onSelect,
  selectDisabled,
  children,
}: {
  projects: readonly T[];
  project: T;
  onSelect: (project: T) => void;
  selectDisabled?: boolean;
  children: ReactNode;
}) {
  return (
    <div className="detection-action" role="group" aria-label="专项检测">
      {children}
      <label
        className="button small detection-action-picker"
        title="选择检测项目"
      >
        <ChevronDown size={14} aria-hidden="true" />
        <select
          aria-label="选择检测项目"
          disabled={selectDisabled}
          value={project}
          onChange={(event) => {
            const next = projects.find((item) => item === event.target.value);
            if (next) onSelect(next);
          }}
        >
          {projects.map((item) => (
            <option key={item} value={item}>
              {detectionProjectLabels[item]}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

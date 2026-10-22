import { useMutation } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { CutPlanSummary, ExportFile, TimelineItem } from "../../api/types";
import { formatSize, timecode } from "../format";

/** One file to download: what it holds, in words. */
export interface FileEntry {
  file: ExportFile;
  label: string;
}

/** Files that belong together (the chat, the candidates, one cut analysis …), with what they are for. */
export interface FileGroup {
  id: string;
  title: string;
  help?: string;
  entries: FileEntry[];
  /** Folded under the group's title (older versions nobody looks at first). */
  folded?: boolean;
}

export const fileName = (f: ExportFile) => f.path.split("/").pop() ?? f.path;

const cutFileText: Record<string, string> = {
  "cuts.edl": "컷 구간 EDL",
  "cuts.csv": "컷 구간 CSV",
  "plan.json": "컷 분석 JSON",
};

const confirmedText: Record<string, string> = {
  "confirmed.edl": "컷 확정본 EDL",
  "confirmed.csv": "컷 확정본 CSV",
  "confirmed.editnest.json": "컷 확정본 editnest 타임라인 메모 JSON",
};

const aiText: Record<string, string> = {
  "recommendations.txt": "AI 추천 TXT",
  "recommendations.json": "AI 추천 JSON",
  "manifest.json": "AI 검토 기록 JSON",
};

/** Entries for the files of a folder that have a name in words, in the order of the names. */
export function entriesIn(files: ExportFile[], folder: string, text: Record<string, string>): FileEntry[] {
  const order = Object.keys(text);
  return files
    .filter((f) => f.path === folder + fileName(f) && text[fileName(f)])
    .sort((a, b) => order.indexOf(fileName(a)) - order.indexOf(fileName(b)))
    .map((f) => ({ file: f, label: text[fileName(f)]! }));
}

/** The newest AI review's files. */
export function aiGroup(files: ExportFile[], aiRunId: string | undefined): FileGroup {
  return {
    id: "ai",
    title: "AI 검토",
    help: "마지막 AI 검토의 설명·추천과 보낸 내용 기록입니다.",
    entries: aiRunId ? entriesIn(files, `ai/${aiRunId}/`, aiText) : [],
  };
}

/** Cut analysis folders whose analysis the candidate list no longer shows (replaced by a newer version). */
export function olderCutFolders(files: ExportFile[], items: TimelineItem[]): string[] {
  const shown = new Set(items.flatMap((it) => (it.kind === "cut" && it.cut ? [it.cut.planId] : [])));
  const folders = new Set(files.map((f) => /^cuts\/([^/]+)\/[^/]+$/.exec(f.path)?.[1]).filter((x): x is string => !!x));
  return [...folders].filter((id) => !shown.has(id));
}

/**
 * The cut analysis files: the confirmed cuts an editing program reads first,
 * then each analysis shown in the candidate list, named by its moment and
 * version, and the folders of replaced versions folded at the end, each
 * named the same way from the saved plans (older summaries) when known.
 */
export function cutGroups(files: ExportFile[], items: TimelineItem[], longForm: boolean, older: CutPlanSummary[] = []): FileGroup[] {
  const groups: FileGroup[] = [
    {
      id: "confirmed",
      title: "컷 확정본",
      help: "확정한 컷 구간입니다. EDL은 편집 프로그램에, editnest 타임라인 메모 JSON은 editnest의 구간 받기에 넣습니다.",
      entries: entriesIn(files, "cuts/", confirmedText),
    },
  ];
  const plans = new Map<string, TimelineItem>();
  for (const it of items) if (it.kind === "cut" && it.cut) plans.set(it.cut.planId, it);
  const folders = [...new Set(files.map((f) => /^cuts\/([^/]+)\/[^/]+$/.exec(f.path)?.[1]).filter((x): x is string => !!x))];
  const at = (ms: number) => timecode(ms, longForm);
  const shown = folders
    .filter((id) => plans.has(id))
    .sort((a, b) => plans.get(a)!.clipStartMs - plans.get(b)!.clipStartMs);
  for (const id of shown) {
    const it = plans.get(id)!;
    groups.push({
      id: "cut-" + id,
      title: `컷 분석 v${it.cut!.version} (${at(it.clipStartMs)}–${at(it.clipEndMs)})`,
      help: it.cut!.target || undefined,
      entries: entriesIn(files, `cuts/${id}/`, cutFileText),
    });
  }
  // Replaced versions, oldest moment first, each folded under its own name.
  const summaries = new Map(older.map((p) => [p.id, p]));
  const rest = folders
    .filter((id) => !plans.has(id))
    .sort((a, b) => (summaries.get(a)?.startMs ?? Infinity) - (summaries.get(b)?.startMs ?? Infinity) || (summaries.get(a)?.version ?? 0) - (summaries.get(b)?.version ?? 0));
  for (const id of rest) {
    const p = summaries.get(id);
    groups.push({
      id: "cut-" + id,
      title: p ? `이전 컷 분석 v${p.version} (${at(p.startMs)}–${at(p.endMs)})` : `이전 컷 분석 (${id.slice(0, 8)})`,
      help: "새 버전으로 바뀌었거나 지금 분석에 후보가 없는 컷 분석입니다.",
      entries: entriesIn(files, `cuts/${id}/`, cutFileText),
      folded: true,
    });
  }
  return groups;
}

interface Props {
  sessionId: string;
  groups: FileGroup[];
  /** What to say when there is nothing yet. */
  empty: string;
}

/**
 * Exported files in groups, each file a download button with what it holds
 * in words, its file name and size. Empty groups are left out.
 */
export function ExportFiles({ sessionId, groups, empty }: Props) {
  const download = useMutation({ mutationFn: (f: ExportFile) => api.download(sessionId, f.path) });
  const shown = groups.filter((g) => g.entries.length > 0);
  const list = (g: FileGroup) => (
    <ul className="files" aria-labelledby={`files-${g.id}`}>
      {g.entries.map(({ file, label }) => (
        <li key={file.path}>
          <button type="button" className="link-button" onClick={() => download.mutate(file)}>
            {label} <span className="file-name">{fileName(file)}</span>
          </button>{" "}
          <span className="file-size">{formatSize(file.size)}</span>
        </li>
      ))}
    </ul>
  );
  return (
    <>
      {shown.length === 0 ? (
        <p className="empty">{empty}</p>
      ) : (
        <div className="file-groups">
          {shown.map((g) =>
            g.folded ? (
              <details key={g.id} className="file-group">
                <summary id={`files-${g.id}`} className="file-group-title">
                  {g.title}
                </summary>
                {g.help && <p className="field-help">{g.help}</p>}
                {list(g)}
              </details>
            ) : (
              <section key={g.id} className="file-group" aria-labelledby={`files-${g.id}`}>
                <h3 id={`files-${g.id}`} className="file-group-title">
                  {g.title}
                </h3>
                {g.help && <p className="field-help">{g.help}</p>}
                {list(g)}
              </section>
            ),
          )}
        </div>
      )}
      {download.isError && (
        <p className="error" role="alert">
          {download.error.message}
        </p>
      )}
    </>
  );
}

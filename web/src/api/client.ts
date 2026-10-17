import type {
  AIComparison,
  AIDelivery,
  AIKeySource,
  AIModel,
  AIPreview,
  AIProvider,
  AIRun,
  AITokenCount,
  Analysis,
  ApiErrorBody,
  Candidate,
  CandidateChats,
  CommentsView,
  ExportFile,
  Checkpoint,
  JobStatus,
  LiveChat,
  LiveDensity,
  LiveInfo,
  LiveStatus,
  Playback,
  PlayheadChats,
  Session,
  Settings,
  SettingsInfo,
  Status,
  UnifiedTimeline,
} from "./types";

export const KEY_HEADER = "X-MomentForge-Key";

/** The per-run key the Go server writes into index.html. */
export function accessKey(): string {
  const meta = document.querySelector<HTMLMetaElement>('meta[name="mf-access-key"]');
  const key = meta?.content ?? "";
  return key.startsWith("__") ? "" : key;
}

/** An API failure with the server's code and user-facing message. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set(KEY_HEADER, accessKey());
  if (init.body) headers.set("Content-Type", "application/json");
  let res: Response;
  try {
    res = await fetch(path, { ...init, headers });
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError(0, "OFFLINE", "서버에 연결할 수 없습니다. MomentForge를 실행한 터미널이 열려 있는지 확인하세요.");
  }
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as ApiErrorBody | null;
    throw new ApiError(
      res.status,
      body?.error.code ?? "HTTP_" + res.status,
      body?.error.message ?? `요청이 실패했습니다 (HTTP ${res.status}).`,
    );
  }
  return res;
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await apiFetch(path);
  return (await res.json()) as T;
}

async function postJSON<T>(path: string, body: unknown = {}): Promise<T> {
  const res = await apiFetch(path, { method: "POST", body: JSON.stringify(body) });
  return (await res.json()) as T;
}

const enc = encodeURIComponent;

export const api = {
  status: () => getJSON<Status>("/api/status"),
  job: () => getJSON<JobStatus>("/api/job"),
  cancelJob: () => postJSON<JobStatus>("/api/job/cancel"),
  collect: (url: string, sensitive: boolean, includeComments = true) =>
    postJSON<JobStatus>("/api/collect", { url, sensitive, includeComments }),
  startComments: (req: { sessionId?: string; url?: string; text?: string }) => postJSON<JobStatus>("/api/comments", req),
  comments: (sessionId: string) => getJSON<CommentsView | null>(`/api/sessions/${enc(sessionId)}/comments`),
  timeline: (sessionId: string) => getJSON<UnifiedTimeline>(`/api/sessions/${enc(sessionId)}/timeline`),
  aiProviders: () => getJSON<{ providers: AIProvider[] }>("/api/ai"),
  /** With persist the key is also kept in the data folder's .env; an empty key clears it everywhere. */
  aiKey: (provider: string, key: string, persist = false) =>
    postJSON<{ configured: boolean; keySource: AIKeySource | "" }>("/api/ai/key", { provider, key, persist }),
  settings: () => getJSON<SettingsInfo>("/api/settings"),
  saveSettings: (s: Settings) => postJSON<SettingsInfo>("/api/settings", s),
  aiModels: (provider: string) => postJSON<{ models: AIModel[] }>("/api/ai/models", { provider }),
  aiPreview: (req: {
    sessionId: string;
    provider: string;
    model: string;
    mode: "candidates" | "all" | "range";
    itemIds?: string[];
    startMs?: number;
    endMs?: number;
    delivery: AIDelivery;
    costCapUsd?: number;
  }) => postJSON<AIPreview>("/api/ai/preview", req),
  aiCount: (previewId: string, fingerprint: string) =>
    postJSON<AITokenCount>("/api/ai/preview/count", { previewId, fingerprint }),
  aiRuns: (sessionId: string) => getJSON<{ runs: AIRun[] }>(`/api/sessions/${enc(sessionId)}/ai/runs`),
  aiCompare: (a: string, b: string) => getJSON<AIComparison>(`/api/ai/compare?a=${enc(a)}&b=${enc(b)}`),
  aiStart: (previewId: string, fingerprint: string, regenerate = false) =>
    postJSON<{ id: string }>("/api/ai/runs", { previewId, fingerprint, regenerate }),
  aiRun: (id: string) => getJSON<AIRun>(`/api/ai/runs/${enc(id)}`),
  aiCancel: (id: string) => postJSON<{ cancelling: boolean }>(`/api/ai/runs/${enc(id)}/cancel`),
  demo: (sensitive = false) => postJSON<Session>("/api/demo", { sensitive }),
  sessions: () => getJSON<Session[]>("/api/sessions"),
  hideSessions: () => postJSON<{ changed: number }>("/api/sessions/hide"),
  restoreSessions: () => postJSON<{ changed: number }>("/api/sessions/restore"),
  session: (id: string) => getJSON<Session>(`/api/sessions/${enc(id)}`),
  resume: (id: string, sensitive: boolean) => postJSON<JobStatus>(`/api/sessions/${enc(id)}/resume`, { sensitive }),
  reanalyze: (id: string, sensitive: boolean) =>
    postJSON<JobStatus>(`/api/sessions/${enc(id)}/analyses`, { sensitive }),
  analysis: (id: string) => getJSON<Analysis>(`/api/analyses/${enc(id)}`),
  candidateChats: (analysisId: string, candidateId: string, offset = 0) =>
    getJSON<CandidateChats>(`/api/analyses/${enc(analysisId)}/candidates/${enc(candidateId)}/chats?offset=${offset}`),
  chatsAt: (sessionId: string, atMs: number, analysisId?: string) =>
    getJSON<PlayheadChats>(
      `/api/sessions/${enc(sessionId)}/chats?at=${Math.floor(atMs)}${analysisId ? `&analysis=${enc(analysisId)}` : ""}`,
    ),
  playback: (sessionId: string) => getJSON<Playback>(`/api/sessions/${enc(sessionId)}/playback`),
  files: (sessionId: string) => getJSON<ExportFile[]>(`/api/sessions/${enc(sessionId)}/files`),
  inspectLive: (url: string) => postJSON<LiveInfo>("/api/live/inspect", { url }),
  startLive: (url: string, sensitive: boolean, viewerAdaptive: boolean) =>
    postJSON<Session>("/api/live", { url, sensitive, viewerAdaptive }),
  liveStatus: (id: string) => getJSON<LiveStatus>(`/api/live/${enc(id)}`),
  stopLive: (id: string) => postJSON<{ stopping: boolean }>(`/api/live/${enc(id)}/stop`),
  resumeLive: (id: string) => postJSON<Session>(`/api/live/${enc(id)}/resume`),
  liveChats: (id: string, afterSeq = 0) =>
    getJSON<{ chats: LiveChat[] }>(`/api/live/${enc(id)}/chats${afterSeq > 0 ? `?after=${afterSeq}` : ""}`),
  liveCandidates: (id: string, afterRevision = 0) =>
    getJSON<{ candidates: Candidate[]; revision: number }>(`/api/live/${enc(id)}/candidates?after=${afterRevision}`),
  liveCandidateChats: (id: string, candidateId: string, offset = 0) =>
    getJSON<CandidateChats>(`/api/live/${enc(id)}/candidates/${enc(candidateId)}/chats?offset=${offset}`),
  liveCheckpoint: (id: string) => postJSON<Checkpoint>(`/api/live/${enc(id)}/checkpoint`),
  liveDensity: (id: string) => getJSON<LiveDensity>(`/api/live/${enc(id)}/density`),
  /** Downloads through fetch so the access key stays in a header, not the URL. */
  async download(sessionId: string, path: string) {
    const res = await apiFetch(`/api/sessions/${enc(sessionId)}/files/${path.split("/").map(enc).join("/")}`);
    const url = URL.createObjectURL(await res.blob());
    const a = document.createElement("a");
    a.href = url;
    a.download = path.split("/").pop() ?? "download";
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10_000);
  },
};

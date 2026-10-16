// Response shapes of the Go API (internal/api). Keep in sync with the JSON tags there.

export interface Status {
  name: string;
  version: string;
  schemaVersion: number;
  startedAt: string;
  dataDir: string;
  platforms: string[];
}

export interface ApiErrorBody {
  error: { code: string; message: string };
}

export interface ServerEvent<T = unknown> {
  type: string;
  at: string;
  data?: T;
}

export type JobPhase = "starting" | "checking" | "collecting" | "analyzing" | "done" | "paused" | "error";

export interface JobStatus {
  id: string;
  kind: "collect" | "analyze" | "";
  busy: boolean;
  phase: JobPhase | "";
  message: string;
  code?: string;
  sessionId?: string;
  title?: string;
  positionMs: number;
  durationMs: number;
  pages: number;
  analysisId?: string;
  candidates: number;
  warning?: string;
  updatedAt: string;
}

export type SessionState = "collecting" | "paused" | "finished";

export interface Session {
  id: string;
  platform: string;
  platformName: string;
  kind: string;
  targetId: string;
  title: string;
  durationMs: number;
  reportedDurationMs: number;
  state: SessionState;
  cursor: number;
  pages: number;
  chatCount: number;
  errorCode?: string;
  errorMessage?: string;
  latestAnalysisId?: string;
  createdAt: string;
  updatedAt: string;
  isDemo: boolean;
  canResume: boolean;
  canPlay: boolean;
  watchUrl?: string;
}

export interface DetectWindow {
  t: number;
  n: number;
  u: number;
  ratio?: number;
  z?: number;
  baselineN?: number;
  baselineReady: boolean;
  valid: boolean;
  signals: string[];
  reactionRatios?: Record<string, number>;
}

export interface Chat {
  id: string;
  offsetMs: number;
  author: string;
  text: string;
}

export interface EvidenceChat extends Chat {
  /** Evidence messages with the same text. */
  count: number;
}

export interface ContextChat extends Chat {
  isEvidence: boolean;
}

export interface Candidate {
  id: string;
  groupId: string;
  reactionStartMs: number;
  reactionEndMs: number;
  peakMs: number;
  clipStartMs: number;
  clipEndMs: number;
  signals: string[];
  reasons: string[];
  peak: { n: number; u: number; ratio?: number; z?: number; baselineN?: number };
  baselineReady: boolean;
  evidenceIds: string[];
  partialContext: boolean;
  evidence: EvidenceChat[];
  evidenceTotal: number;
  watchUrl?: string;
}

export interface Analysis {
  id: string;
  sessionId: string;
  title: string;
  durationMs: number;
  rulesVersion: string;
  sensitive: boolean;
  windows: DetectWindow[];
  candidates: Candidate[];
}

export interface PlayheadChats {
  chats: Chat[];
  startMs: number;
  endMs: number;
  atMs: number;
  truncated: boolean;
}

export interface CandidateChats {
  chats: ContextChat[];
  total: number;
  nextOffset: number | null;
}

export interface ExportFile {
  path: string;
  size: number;
}

export interface Playback {
  url: string;
  type: "hls";
}

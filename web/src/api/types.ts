// Response shapes of the Go API (internal/api). Keep in sync with the JSON tags there.

export interface Status {
  name: string;
  version: string;
  schemaVersion: number;
  startedAt: string;
  dataDir: string;
}

export interface ApiErrorBody {
  error: { code: string; message: string };
}

export interface ServerEvent<T = unknown> {
  type: string;
  at: string;
  data?: T;
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

export interface EvidenceChat {
  offsetMs: number;
  author: string;
  text: string;
  /** Evidence messages with the same text. */
  count: number;
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
}

export interface Analysis {
  title: string;
  durationMs: number;
  rulesVersion: string;
  windows: DetectWindow[];
  candidates: Candidate[];
}

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

export type LiveState = "starting" | "collecting" | "reconnecting" | "stopping" | "stopped" | "ended" | "error" | "incomplete";

export type SessionState = "collecting" | "paused" | "finished" | LiveState;

export interface Session {
  id: string;
  platform: string;
  platformName: string;
  kind: "vod" | "live";
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
  /** Adaptive gate: messages that make a window busy for the broadcast (2-hour median × 3.75). */
  gate?: number;
  /** Adaptive gate: chatters that make a window a sudden spike (15-minute median × 5). */
  spikeGate?: number;
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

export type CandidateState = "generating" | "waiting_context" | "confirmed" | "partial";

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
  peak: { n: number; u: number; ratio?: number; z?: number; baselineN?: number; gate?: number; spikeGate?: number };
  baselineReady: boolean;
  evidenceIds: string[];
  partialContext: boolean;
  evidence: EvidenceChat[];
  evidenceTotal: number;
  watchUrl?: string;
  /** LIVE only: how settled the candidate is. */
  state?: CandidateState;
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

// --- LIVE ---

export interface LiveInfo {
  platformName: string;
  channelId: string;
  liveId: string;
  title: string;
  open: boolean;
  viewers: number | null;
  startedAt: string | null;
}

export interface Gap {
  startMs: number;
  endMs: number;
}

export interface LiveStatus {
  session: Session;
  channelId: string;
  liveId: string;
  timeBasis: "broadcast_elapsed" | "capture_elapsed";
  running: boolean;
  sensitive: boolean;
  /** The message gate follows the channel (REDESIGN 6.2.1); false for older captures. */
  adaptiveGate: boolean;
  viewerAdaptive: boolean;
  /** Analysis is final up to here. */
  watermarkMs: number;
  /** Connections made so far. */
  generation: number;
  viewerCount: number | null;
  viewerStatus: "" | "active" | "warming_up" | "missing";
  lateCount: number;
  duplicateCount: number;
  invalidTimeCount: number;
  revision: number;
  closedReason?: string;
  gaps: Gap[];
  lastWindow: DetectWindow | null;
}

export interface LiveChat extends Chat {
  seq: number;
  /** Stored, but arrived too late (or from before the connection) to count in analysis. */
  late: boolean;
}

/** Payload of the "live" server event, sent after each save. */
export interface LiveUpdate {
  sessionId: string;
  state: LiveState;
  watermarkMs: number;
  generation: number;
  revision: number;
  message?: string;
  chats: LiveChat[];
}

export interface Checkpoint {
  id: string;
  folder: string;
  eventCount: number;
}

/** Messages per bucket over the capture so far; invalid windows are unobserved gaps. */
export interface LiveDensity {
  bucketMs: number;
  endMs: number;
  windows: DetectWindow[];
}

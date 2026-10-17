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

export type JobPhase = "starting" | "checking" | "collecting" | "analyzing" | "comments" | "done" | "paused" | "error";

export interface JobStatus {
  id: string;
  kind: "collect" | "analyze" | "comments" | "";
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
  /** Comments read and viewer recommendations found (comments step). */
  comments?: number;
  recommendations?: number;
  warning?: string;
  updatedAt: string;
}

export type LiveState = "starting" | "collecting" | "reconnecting" | "stopping" | "stopped" | "ended" | "error" | "incomplete";

export type SessionState = "collecting" | "paused" | "finished" | "comments_only" | LiveState;

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
  canReadComments: boolean;
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

// --- Viewer recommendations, unified timeline, AI (phase 4) ---

export interface ViewerRecommendation {
  id: string;
  commentId: string;
  startMs: number;
  endMs: number | null;
  description: string;
  commentText: string;
  watchUrl?: string;
}

export interface CommentsView {
  captureId: string;
  source: "website" | "manual_paste";
  state: "finished" | "partial";
  pages: number;
  reportedCount: number | null;
  commentCount: number;
  issues: string[];
  createdAt: string;
  recommendations: ViewerRecommendation[];
  needsReview: { commentId: string; text: string; reasons: string[] }[];
}

export type TimelineSource = "rule" | "viewer_comment" | "ai";

export interface TimelineItem {
  id: string;
  source: TimelineSource;
  originId: string;
  anchorMs: number;
  clipStartMs: number;
  clipEndMs: number;
  title: string;
  commentText?: string;
  rationale?: string;
  uncertainty?: string;
  priority?: "high" | "medium" | "low";
  watchUrl?: string;
}

export interface UnifiedTimeline {
  sessionId: string;
  title: string;
  durationMs: number;
  analysisId: string;
  captureId: string;
  aiRunId: string;
  items: TimelineItem[];
}

export interface AIProvider {
  id: string;
  name: string;
  /** A key is in memory, or (selfAuth) the provider's own program is there. */
  configured: boolean;
  /** Signs in with another program (OpenCode CLI); no key goes into MomentForge. */
  selfAuth: boolean;
  authHelp?: string;
  /** Why a selfAuth provider cannot be used now. */
  notReady?: string;
  /** Filled in for the user; empty when the user must enter a model. */
  defaultModel: string;
  canCount: boolean;
  canListModels: boolean;
  canBatch: boolean;
  hasPrices: boolean;
  /** Where the key came from: the environment, the data folder's .env, or entered for this run. */
  keySource?: AIKeySource;
  /** The key's environment variable and its line in the data folder's .env. */
  keyName?: string;
  /** Where requests go, for a provider that sends to a proxy the user runs (CLIProxyAPI, OpenCodex). */
  endpoint?: string;
  defaultEndpoint?: string;
  /** What the address is, shown under it in 설정. */
  endpointHelp?: string;
  /** For a key found in the provider's own program config (keySource "tool"): that file. */
  keyFoundIn?: string;
  /** Sends to a local program MomentForge can start and stop (CLIProxyAPI, OpenCodex). */
  hasService: boolean;
}

/** How a provider's local program stands (CLIProxyAPI, OpenCodex). */
export interface AIService {
  installed: boolean;
  /** Something answers at the provider's address. */
  running: boolean;
  /** MomentForge can stop it: it started it, or the program's pid file names it. */
  managed: boolean;
  program?: string;
  config?: string;
  log?: string;
  note?: string;
  /** The program's own dashboard, when it runs with it turned on. */
  dashboard?: string;
  /** It runs, but its dashboard is turned off in its config. */
  dashboardOff: boolean;
  /** How to sign in to the dashboard, or (dashboardOff) how to turn it on. */
  dashboardHelp?: string;
}

export type AIKeySource = "env" | "file" | "memory" | "tool";

/** Defaults the screens start from (the data folder's settings.env). Nothing secret. */
export interface Settings {
  /** "" for the first provider. */
  aiProvider: string;
  /** Provider ID → model; a missing one uses the provider's own default. */
  aiModels: Record<string, string>;
  /** Provider ID → address, for providers that send to a proxy; a missing one uses the provider's default. */
  aiEndpoints: Record<string, string>;
  sensitive: boolean;
  includeComments: boolean;
  viewerAdaptive: boolean;
}

export interface SettingsInfo extends Settings {
  /** settings.env */
  file: string;
  /** .env: only the keys the user chose to keep on this computer. */
  keyFile: string;
  /** No provider is saved: aiProvider is the one this computer could use when the app started. */
  aiProviderAuto: boolean;
}

export interface AIModel {
  id: string;
  name: string;
}

export type AIDelivery = "single" | "batch";

/** List price in US dollars per million tokens. */
export interface AIPrice {
  input: number;
  output: number;
  cache_read: number;
  cache_write: number;
  batch_share: number;
  as_of: string;
}

/** Exactly what an AI run will send (snake_case as sent). */
export interface AIPlan {
  provider: string;
  model: string;
  delivery: AIDelivery;
  prompt: string;
  ranges: { start_ms: number; end_ms: number }[];
  chunks: unknown[];
  omitted: { start_ms: number; end_ms: number; reason: string }[];
  request_count: number;
  planned_input_token_estimate: number;
  input_token_upper_estimate: number;
  output_token_limit: number;
  request_attempt_limit: number;
  estimate_note: string;
  /** Absent when the model's price is unknown. */
  price?: AIPrice;
  cost_upper_usd?: number;
  cost_cap_usd?: number;
  fingerprint: string;
}

export interface AITokenCount {
  inputTokens: number;
  costUpperUsd: number;
  hasPrice: boolean;
}

export interface AIPreview {
  previewId: string;
  expiresAt: string;
  plan: AIPlan;
}

export type AIRunState = "running" | "completed" | "partial" | "failed" | "cancelled";

export interface AIRun {
  id: string;
  sessionId: string;
  provider: string;
  model: string;
  delivery: AIDelivery;
  state: AIRunState;
  requestCount: number;
  completedRequests: number;
  attempts: number;
  rejected: number;
  recommendations: number;
  /** All input tokens, cached or not. */
  inputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  outputTokens: number;
  /** At list price; null when the price is unknown. */
  costUsd: number | null;
  costCapUsd: number | null;
  /** A submitted batch needs the key again to fetch its results. */
  waitingForKey: boolean;
  errorCode?: string;
  errorMessage?: string;
  createdAt: string;
}

export interface AIRecommendation {
  id: string;
  sourceCandidateIds: string[];
  startMs: number;
  endMs: number;
  title: string;
  rationale: string;
  uncertainty: string;
  priority: "high" | "medium" | "low";
}

export interface AIComparison {
  a: AIRun;
  b: AIRun;
  /** Both runs sent exactly the same chats. */
  sameInput: boolean;
  matched: { a: AIRecommendation; b: AIRecommendation; overlapMs: number }[];
  onlyA: AIRecommendation[];
  onlyB: AIRecommendation[];
}

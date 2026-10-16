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

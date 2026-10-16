import type { Analysis, ApiErrorBody, Status } from "./types";

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
  let res: Response;
  try {
    res = await fetch(path, { ...init, headers });
  } catch {
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

export const api = {
  status: () => getJSON<Status>("/api/status"),
  demo: () => getJSON<Analysis>("/api/demo"),
};

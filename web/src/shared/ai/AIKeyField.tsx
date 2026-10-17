import { useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { AIProvider } from "../../api/types";

/** Where the key in use came from, and what clearing it does. */
function sourceText(p: AIProvider): string {
  switch (p.keySource) {
    case "file":
      return "이 컴퓨터(데이터 폴더의 .env)에 저장한 키를 씁니다. 지우면 파일에서도 지웁니다.";
    case "env":
      return `환경변수 ${p.keyName ?? ""}에서 읽은 키를 씁니다. 지워도 다음 실행 때 다시 읽습니다.`;
    case "memory":
      return "이번 실행 동안만 메모리에 둔 키입니다. 앱을 끄면 지워집니다.";
    default:
      return "";
  }
}

interface Props {
  provider: AIProvider;
  /** More actions on the key's row (loading the model list). */
  extra?: ReactNode;
  /** Called after a key was saved or cleared. */
  onChanged?: () => void;
}

/**
 * Enters, keeps or clears one provider's API key. The settings screen and
 * the AI review tab both use it, so they show the same state. A key goes to
 * server memory; only with "이 컴퓨터에도 저장" is it also written to the
 * data folder's .env. It is never shown again or kept in the browser.
 * Give it key={provider.id} so a typed key never carries over to another provider.
 */
export function AIKeyField({ provider: p, extra, onChanged }: Props) {
  const queryClient = useQueryClient();
  const [key, setKey] = useState("");
  const [keep, setKeep] = useState(p.keySource === "file");
  const save = useMutation({
    mutationFn: (value: string) => api.aiKey(p.id, value, keep),
    onSuccess: () => {
      setKey("");
      void queryClient.invalidateQueries({ queryKey: ["aiProviders"] });
      onChanged?.();
    },
  });
  return (
    <div className="key-field">
      <label className="field">
        <span className="field-label">
          {p.name} API 키 {p.configured ? "(메모리에 있음)" : "(없음)"}
        </span>
        <input
          type="password"
          autoComplete="off"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            e.preventDefault();
            if (key.trim() && !save.isPending) save.mutate(key);
          }}
        />
      </label>
      <label className="check check-inline">
        <input type="checkbox" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
        이 컴퓨터에도 저장 (다음 실행에도 씀)
      </label>
      {keep && (
        <p className="field-help">키를 데이터 폴더의 .env에 평문으로 저장합니다. 이 폴더를 다른 사람과 공유하거나 동기화하지 마세요.</p>
      )}
      <p className="actions">
        <button type="button" className="button button-quiet" onClick={() => save.mutate(key)} disabled={!key.trim() || save.isPending}>
          키 저장
        </button>
        {p.configured && (
          <button type="button" className="link-button" onClick={() => save.mutate("")} disabled={save.isPending}>
            키 지우기
          </button>
        )}
        {extra}
      </p>
      {p.configured && <p className="field-help">{sourceText(p)}</p>}
      {save.isError && (
        <p className="error" role="alert">
          {save.error.message}
        </p>
      )}
    </div>
  );
}

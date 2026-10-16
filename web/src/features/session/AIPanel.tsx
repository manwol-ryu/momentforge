import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { AIPreview, AIRun, Session } from "../../api/types";
import { useEvents } from "../../shared/events";
import { timecode } from "../../shared/format";

const runStateText: Record<AIRun["state"], string> = {
  running: "보내는 중",
  completed: "완료",
  partial: "일부 완료",
  failed: "실패",
  cancelled: "중단됨",
};

const omittedText: Record<string, string> = {
  comment_input_too_large: "댓글이 너무 김",
  message_too_large: "채팅 한 건이 너무 김",
  input_budget: "요청 크기 한도",
  request_budget: "요청 횟수 한도(20회)",
  retry_request_budget: "재시도 한도",
};

/** "1:02:03", "12:30" or seconds → milliseconds; NaN when unreadable. */
function parseTime(text: string): number {
  const t = text.trim();
  if (/^\d+$/.test(t)) return Number(t) * 1000;
  const m = /^(?:(\d+):)?(\d{1,2}):(\d{2})$/.exec(t);
  if (!m) return NaN;
  return ((Number(m[1] ?? 0) * 60 + Number(m[2])) * 60 + Number(m[3])) * 1000;
}

interface Props {
  s: Session;
  /** Timeline item IDs ticked in the candidate list. */
  selection: string[];
}

/**
 * Opt-in AI review (CzzEdit's 선택형 AI): the plan to send is shown as is,
 * and nothing is sent until the user confirms that exact plan.
 */
export function AIPanel({ s, selection }: Props) {
  const queryClient = useQueryClient();
  const { subscribe } = useEvents();
  const providers = useQuery({ queryKey: ["aiProviders"], queryFn: api.aiProviders });
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [key, setKey] = useState("");
  const [mode, setMode] = useState<"candidates" | "range">("candidates");
  const [from, setFrom] = useState("0:00:00");
  const [to, setTo] = useState(timecode(s.durationMs, true));
  const [preview, setPreview] = useState<AIPreview | null>(null);
  const [runId, setRunId] = useState<string | null>(null);

  const list = providers.data?.providers ?? [];
  const current = list.find((p) => p.id === provider) ?? list[0];
  // Any change to what would be sent drops the preview.
  const inputs = useMemo(() => JSON.stringify([current?.id, model, mode, from, to, selection]), [current?.id, model, mode, from, to, selection]);
  useEffect(() => setPreview(null), [inputs]);

  const saveKey = useMutation({
    mutationFn: (value: string) => api.aiKey(current!.id, value),
    onSuccess: () => {
      setKey("");
      void queryClient.invalidateQueries({ queryKey: ["aiProviders"] });
    },
  });
  const makePreview = useMutation({
    mutationFn: () =>
      api.aiPreview({
        sessionId: s.id,
        provider: current!.id,
        model,
        mode,
        itemIds: mode === "candidates" ? selection : undefined,
        startMs: mode === "range" ? parseTime(from) : undefined,
        endMs: mode === "range" ? parseTime(to) : undefined,
      }),
    onSuccess: setPreview,
  });
  const start = useMutation({
    mutationFn: () => api.aiStart(preview!.previewId, preview!.plan.fingerprint),
    onSuccess: (r) => setRunId(r.id),
  });
  const run = useQuery({ queryKey: ["aiRun", runId], queryFn: () => api.aiRun(runId!), enabled: !!runId });
  const cancel = useMutation({ mutationFn: () => api.aiCancel(runId!) });

  useEffect(
    () =>
      subscribe("ai", (event) => {
        const r = event.data as AIRun;
        if (r.sessionId !== s.id) return;
        queryClient.setQueryData(["aiRun", r.id], r);
        if (r.state !== "running") void queryClient.invalidateQueries({ queryKey: ["timeline", s.id] });
      }),
    [subscribe, queryClient, s.id],
  );

  const r = run.data;
  const failed = makePreview.error ?? start.error ?? saveKey.error ?? cancel.error;
  if (list.length === 0) return null;
  return (
    <section className="section" aria-labelledby="ai-title">
      <h2 id="ai-title" className="section-title">
        선택형 AI 검토
      </h2>
      <p className="field-help">
        고른 후보 앞뒤 채팅이나 지정한 구간의 채팅만 외부 AI API로 보냅니다. 보내기 전에 보낼 내용을 그대로 보여 드리고,
        확인해야 보냅니다. API 키는 앱이 실행되는 동안 메모리에만 둡니다. 저장하지 말라고(store=false) 요청하지만 제공자의 보관
        정책이 따로 적용될 수 있습니다.
      </p>
      <div className="ai-grid">
        {list.length > 1 && (
          <label className="field">
            <span className="field-label">AI 제공자</span>
            <select value={current?.id} onChange={(e) => setProvider(e.target.value)}>
              {list.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="field">
          <span className="field-label">모델 ID</span>
          <input type="text" value={model} onChange={(e) => setModel(e.target.value)} placeholder="계정에서 쓸 수 있는 모델 ID" />
        </label>
        <label className="field">
          <span className="field-label">
            {current?.name} API 키 {current?.configured ? "(메모리에 있음)" : "(없음)"}
          </span>
          <input type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} />
        </label>
      </div>
      <p className="actions">
        <button type="button" className="button button-quiet" onClick={() => saveKey.mutate(key)} disabled={!key.trim() || saveKey.isPending}>
          키 저장
        </button>
        {current?.configured && (
          <button type="button" className="link-button" onClick={() => saveKey.mutate("")} disabled={saveKey.isPending}>
            메모리의 키 지우기
          </button>
        )}
      </p>
      <fieldset className="ai-mode">
        <legend className="field-label">보낼 범위</legend>
        <label className="check">
          <input type="radio" name="ai-mode" checked={mode === "candidates"} onChange={() => setMode("candidates")} />
          후보 목록에서 고른 {selection.length}개 (편집 구간과 앞뒤 채팅)
        </label>
        <label className="check">
          <input type="radio" name="ai-mode" checked={mode === "range"} onChange={() => setMode("range")} />
          채팅 구간 직접 지정
        </label>
        {mode === "range" && (
          <span className="ai-range">
            <input type="text" aria-label="시작 시각" value={from} onChange={(e) => setFrom(e.target.value)} /> ~{" "}
            <input type="text" aria-label="끝 시각" value={to} onChange={(e) => setTo(e.target.value)} />
          </span>
        )}
      </fieldset>
      <p className="actions">
        <button
          type="button"
          className="button button-quiet"
          onClick={() => makePreview.mutate()}
          disabled={makePreview.isPending || !model.trim() || (mode === "candidates" && selection.length === 0)}
        >
          보낼 내용 미리보기
        </button>
      </p>
      {preview && (
        <div className="ai-preview" role="status">
          <p>
            요청 {preview.plan.request_count}회, 입력 추정 약 {preview.plan.planned_input_token_estimate.toLocaleString("ko-KR")}토큰
            (상한 {preview.plan.input_token_upper_estimate.toLocaleString("ko-KR")}), 출력 상한{" "}
            {preview.plan.output_token_limit.toLocaleString("ko-KR")}토큰, 재시도 포함 최대 {preview.plan.request_attempt_limit}회.{" "}
            {preview.plan.omitted.length > 0 &&
              `보내지 않는 부분 ${preview.plan.omitted.length}개(${[...new Set(preview.plan.omitted.map((o) => omittedText[o.reason] ?? o.reason))].join(", ")}). `}
            10분 뒤 만료됩니다.
          </p>
          <p className="field-help">{preview.plan.estimate_note}</p>
          <details className="panel">
            <summary>보낼 내용 전체 (지시문, 구간, 채팅)</summary>
            <pre className="ai-plan">
              {JSON.stringify({ instructions: preview.plan.prompt, ranges: preview.plan.ranges, chunks: preview.plan.chunks, omitted: preview.plan.omitted }, null, 2)}
            </pre>
          </details>
          <p className="actions">
            <button
              type="button"
              className="button"
              onClick={() => start.mutate()}
              disabled={start.isPending || !current?.configured || r?.state === "running"}
            >
              확인한 내용으로 AI 실행
            </button>
            {!current?.configured && <span className="field-help">먼저 API 키를 저장하세요.</span>}
          </p>
        </div>
      )}
      {r && (
        <p className="ai-status" role="status" data-state={r.state}>
          AI {runStateText[r.state]}: {r.completedRequests}/{r.requestCount}회 완료, 추천 {r.recommendations}개
          {r.rejected > 0 && `, 검증에서 뺀 추천 ${r.rejected}개`}
          {r.inputTokens > 0 && `, 토큰 입력 ${r.inputTokens.toLocaleString("ko-KR")}·출력 ${r.outputTokens.toLocaleString("ko-KR")}`}
          {r.errorMessage && ` (${r.errorMessage})`}
          {r.state === "running" && (
            <>
              {" "}
              <button type="button" className="link-button" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
                AI 작업 중단
              </button>
            </>
          )}
        </p>
      )}
      {failed && (
        <p className="error" role="alert">
          {failed.message}
        </p>
      )}
    </section>
  );
}

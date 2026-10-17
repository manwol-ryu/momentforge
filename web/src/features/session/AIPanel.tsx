import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { AIDelivery, AIModel, AIPreview, AIRun, AITokenCount, Session } from "../../api/types";
import { useEvents } from "../../shared/events";
import { timecode, tokens, usd } from "../../shared/format";

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
  request_budget: "요청 횟수 한도",
  retry_request_budget: "재시도 한도",
  cost_cap: "비용 한도",
};

/** "1:02:03", "12:30" or seconds → milliseconds; NaN when unreadable. */
function parseTime(text: string): number {
  const t = text.trim();
  if (/^\d+$/.test(t)) return Number(t) * 1000;
  const m = /^(?:(\d+):)?(\d{1,2}):(\d{2})$/.exec(t);
  if (!m) return NaN;
  return ((Number(m[1] ?? 0) * 60 + Number(m[2])) * 60 + Number(m[3])) * 1000;
}

/** The status line of a run: progress, tokens with cache hits, cost. */
export function runSummary(r: AIRun): string {
  const waiting = r.state === "running" && r.delivery === "batch";
  const parts = [
    waiting ? `배치 결과 기다리는 중: ${r.completedRequests}/${r.requestCount}회 처리` : `${r.completedRequests}/${r.requestCount}회 완료`,
    `추천 ${r.recommendations}개`,
  ];
  if (r.rejected > 0) parts.push(`검증에서 뺀 추천 ${r.rejected}개`);
  if (r.inputTokens > 0) {
    const cache = r.cacheReadTokens > 0 ? `(캐시 적중 ${tokens(r.cacheReadTokens)})` : "";
    parts.push(`토큰 입력 ${tokens(r.inputTokens)}${cache}·출력 ${tokens(r.outputTokens)}`);
  }
  if (r.costUsd != null) parts.push(`비용 ${usd(r.costUsd)}`);
  return parts.join(", ");
}

interface Props {
  s: Session;
  /** Timeline item IDs ticked in the candidate list. */
  selection: string[];
}

/**
 * Opt-in AI review (CzzEdit's 선택형 AI): the plan to send is shown as is,
 * with its most possible cost, and nothing is sent until the user confirms
 * that exact plan.
 */
export function AIPanel({ s, selection }: Props) {
  const queryClient = useQueryClient();
  const { subscribe } = useEvents();
  const providers = useQuery({ queryKey: ["aiProviders"], queryFn: api.aiProviders });
  const runs = useQuery({ queryKey: ["aiRuns", s.id], queryFn: () => api.aiRuns(s.id) });
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [models, setModels] = useState<AIModel[]>([]);
  const [key, setKey] = useState("");
  const [mode, setMode] = useState<"candidates" | "all" | "range">("candidates");
  const [from, setFrom] = useState("0:00:00");
  const [to, setTo] = useState(timecode(s.durationMs, true));
  const [delivery, setDelivery] = useState<AIDelivery>("single");
  const [cap, setCap] = useState("");
  const [preview, setPreview] = useState<AIPreview | null>(null);
  const [counted, setCounted] = useState<AITokenCount | null>(null);
  const [runId, setRunId] = useState<string | null>(null);

  const list = providers.data?.providers ?? [];
  const current = list.find((p) => p.id === provider) ?? list[0];
  // A new provider starts from its own default model and options.
  useEffect(() => {
    setModel(current?.defaultModel ?? "");
    setModels([]);
    if (!current?.canBatch) setDelivery("single");
    if (!current?.hasPrices) {
      // Without prices there is no cap, so no automatic selection either.
      setCap("");
      setMode((m) => (m === "all" ? "candidates" : m));
    }
  }, [current?.id, current?.defaultModel, current?.canBatch, current?.hasPrices]);
  // Any change to what would be sent drops the preview.
  const inputs = useMemo(
    () => JSON.stringify([current?.id, model, mode, from, to, selection, delivery, cap]),
    [current?.id, model, mode, from, to, selection, delivery, cap],
  );
  useEffect(() => {
    setPreview(null);
    setCounted(null);
  }, [inputs]);
  // After a reload, follow a run that is still going (a batch can take hours).
  const running = runs.data?.runs.find((r) => r.state === "running");
  useEffect(() => {
    if (!runId && running) setRunId(running.id);
  }, [runId, running]);

  const capValue = cap.trim() === "" ? undefined : Number(cap);
  const capInvalid = capValue !== undefined && (!Number.isFinite(capValue) || capValue <= 0);

  const saveKey = useMutation({
    mutationFn: (value: string) => api.aiKey(current!.id, value),
    onSuccess: () => {
      setKey("");
      void queryClient.invalidateQueries({ queryKey: ["aiProviders"] });
      void queryClient.invalidateQueries({ queryKey: ["aiRuns", s.id] });
    },
  });
  const loadModels = useMutation({
    mutationFn: () => api.aiModels(current!.id),
    onSuccess: (r) => setModels(r.models),
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
        delivery,
        costCapUsd: capValue,
      }),
    onSuccess: setPreview,
  });
  const count = useMutation({
    mutationFn: () => api.aiCount(preview!.previewId, preview!.plan.fingerprint),
    onSuccess: setCounted,
  });
  const start = useMutation({
    mutationFn: () => api.aiStart(preview!.previewId, preview!.plan.fingerprint),
    onSuccess: (r) => {
      setRunId(r.id);
      void queryClient.invalidateQueries({ queryKey: ["aiRuns", s.id] });
    },
  });
  const run = useQuery({ queryKey: ["aiRun", runId], queryFn: () => api.aiRun(runId!), enabled: !!runId });
  const cancel = useMutation({ mutationFn: () => api.aiCancel(runId!) });

  useEffect(
    () =>
      subscribe("ai", (event) => {
        const r = event.data as AIRun;
        if (r.sessionId !== s.id) return;
        queryClient.setQueryData(["aiRun", r.id], r);
        void queryClient.invalidateQueries({ queryKey: ["aiRuns", s.id] });
        if (r.state !== "running") void queryClient.invalidateQueries({ queryKey: ["timeline", s.id] });
      }),
    [subscribe, queryClient, s.id],
  );

  const r = run.data;
  const failed = makePreview.error ?? start.error ?? saveKey.error ?? cancel.error ?? loadModels.error ?? count.error;
  if (list.length === 0) return null;
  const plan = preview?.plan;
  const needsCap = mode === "all" && capValue === undefined;
  return (
    <section className="section" aria-labelledby="ai-title">
      <h2 id="ai-title" className="section-title">
        선택형 AI 검토
      </h2>
      <p className="field-help">
        고른 후보 앞뒤 채팅이나 지정한 구간의 채팅만 외부 AI API로 보냅니다. 보내기 전에 보낼 내용과 최대 비용을 그대로 보여
        드리고, 확인해야 보냅니다. API 키는 앱이 실행되는 동안 메모리에만 둡니다. 제공자의 데이터 보관 정책이 따로 적용될 수
        있습니다.
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
          <input
            type="text"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            list="ai-models"
            placeholder="계정에서 쓸 수 있는 모델 ID"
          />
          <datalist id="ai-models">
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </datalist>
        </label>
        {current?.selfAuth ? (
          <div className="field">
            <span className="field-label">로그인</span>
            <span className="ai-auth" data-ready={current.configured || undefined}>
              {current.configured ? `${current.name} 로그인 사용` : current.notReady}
            </span>
          </div>
        ) : (
          <label className="field">
            <span className="field-label">
              {current?.name} API 키 {current?.configured ? "(메모리에 있음)" : "(없음)"}
            </span>
            <input type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} />
          </label>
        )}
      </div>
      {current?.selfAuth && current.authHelp && <p className="field-help">{current.authHelp}</p>}
      <p className="actions">
        {!current?.selfAuth && (
          <button type="button" className="button button-quiet" onClick={() => saveKey.mutate(key)} disabled={!key.trim() || saveKey.isPending}>
            키 저장
          </button>
        )}
        {current?.configured && !current.selfAuth && (
          <button type="button" className="link-button" onClick={() => saveKey.mutate("")} disabled={saveKey.isPending}>
            메모리의 키 지우기
          </button>
        )}
        {current?.canListModels && current.configured && (
          <button type="button" className="link-button" onClick={() => loadModels.mutate()} disabled={loadModels.isPending}>
            {models.length > 0 ? `모델 ${models.length}개 불러옴` : "쓸 수 있는 모델 불러오기"}
          </button>
        )}
      </p>
      <fieldset className="ai-mode">
        <legend className="field-label">보낼 범위</legend>
        <label className="check">
          <input type="radio" name="ai-mode" checked={mode === "candidates"} onChange={() => setMode("candidates")} />
          후보 목록에서 고른 {selection.length}개 (편집 구간과 앞뒤 채팅)
        </label>
        {current?.hasPrices && (
          <label className="check">
            <input type="radio" name="ai-mode" checked={mode === "all"} onChange={() => setMode("all")} />
            모든 후보 자동 선택 (비용 한도 안에서만 보냄)
          </label>
        )}
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
      {(current?.canBatch || current?.hasPrices) && (
        <div className="ai-grid">
          {current?.canBatch && (
            <fieldset className="ai-mode">
              <legend className="field-label">전송 방식</legend>
              <label className="check">
                <input type="radio" name="ai-delivery" checked={delivery === "single"} onChange={() => setDelivery("single")} />
                바로 보내기 (한 요청씩, 바로 결과)
              </label>
              <label className="check">
                <input type="radio" name="ai-delivery" checked={delivery === "batch"} onChange={() => setDelivery("batch")} />
                배치로 보내기 (가격 절반, 결과는 보통 1시간 안, 최대 24시간)
              </label>
            </fieldset>
          )}
          {current?.hasPrices && (
            <label className="field ai-cap">
              <span className="field-label">비용 한도 (미국 달러{mode === "all" ? ", 필수" : ", 비우면 없음"})</span>
              <input type="text" inputMode="decimal" value={cap} onChange={(e) => setCap(e.target.value)} placeholder="예: 2" />
            </label>
          )}
        </div>
      )}
      <p className="actions">
        <button
          type="button"
          className="button button-quiet"
          onClick={() => makePreview.mutate()}
          disabled={
            makePreview.isPending || !model.trim() || capInvalid || needsCap || (mode === "candidates" && selection.length === 0)
          }
        >
          보낼 내용 미리보기
        </button>
        {capInvalid && <span className="field-help">비용 한도는 0보다 큰 숫자로 입력하세요.</span>}
        {needsCap && <span className="field-help">모든 후보를 보낼 때는 비용 한도를 정하세요.</span>}
      </p>
      {plan && preview && (
        <div className="ai-preview" role="status">
          <p>
            요청 {plan.request_count}회, 입력 추정 약 {tokens(plan.planned_input_token_estimate)}토큰, 출력 상한{" "}
            {tokens(plan.output_token_limit)}토큰
            {plan.delivery === "batch" ? ". 한 번에 배치로 보냅니다. " : `, 재시도 포함 최대 ${plan.request_attempt_limit}회. `}
            {plan.omitted.length > 0 &&
              `보내지 않는 부분 ${plan.omitted.length}개(${[...new Set(plan.omitted.map((o) => omittedText[o.reason] ?? o.reason))].join(", ")}). `}
            10분 뒤 만료됩니다.
          </p>
          {plan.price ? (
            <p className="ai-cost">
              예상 비용 최대 {usd(plan.cost_upper_usd ?? 0)}
              {plan.cost_cap_usd ? ` (한도 ${usd(plan.cost_cap_usd)})` : ""}. {plan.price.as_of} 공시 가격 기준
              {plan.delivery === "batch" ? ", 배치 50% 할인 적용" : ""}.
            </p>
          ) : (
            <p className="field-help">이 모델의 가격 정보가 없어 비용을 계산하지 않았습니다.</p>
          )}
          <p className="field-help">{plan.estimate_note}</p>
          {current?.canCount && (
            <p className="actions">
              <button type="button" className="link-button" onClick={() => count.mutate()} disabled={count.isPending || !current.configured}>
                정확한 토큰 수 계산
              </button>
              <span className="field-help">
                보낼 채팅을 {current.name}의 토큰 계산 API로 보냅니다. 답을 만들지 않으며 요금이 들지 않습니다.
              </span>
            </p>
          )}
          {counted && (
            <p className="ai-cost">
              정확한 입력 토큰 {tokens(counted.inputTokens)}개
              {counted.hasPrice && `, 예상 비용 최대 ${usd(counted.costUpperUsd)}`}.
            </p>
          )}
          <details className="panel">
            <summary>보낼 내용 전체 (지시문, 구간, 채팅)</summary>
            <pre className="ai-plan">
              {JSON.stringify({ instructions: plan.prompt, ranges: plan.ranges, chunks: plan.chunks, omitted: plan.omitted }, null, 2)}
            </pre>
          </details>
          <p className="actions">
            <button
              type="button"
              className="button"
              onClick={() => start.mutate()}
              disabled={start.isPending || !current?.configured || (r?.state === "running" && r.delivery === "single")}
            >
              확인한 내용으로 AI 실행
            </button>
            {!current?.configured && (
              <span className="field-help">{current?.selfAuth ? current.notReady : "먼저 API 키를 저장하세요."}</span>
            )}
          </p>
        </div>
      )}
      {r && (
        <p className="ai-status" role="status" data-state={r.state}>
          AI {r.state === "running" && r.delivery === "batch" ? "배치" : runStateText[r.state]}: {runSummary(r)}
          {r.waitingForKey && ". API 키를 다시 저장하면 결과를 가져옵니다"}
          {r.errorMessage && ` (${r.errorMessage})`}
          {r.state === "running" && (
            <>
              {" "}
              <button type="button" className="link-button" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
                {r.delivery === "batch" ? "배치 취소" : "AI 작업 중단"}
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

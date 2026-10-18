import { useEffect, useMemo, useState, type CSSProperties } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { AIDelivery, AIModel, AIPreview, AIRun, AITokenCount, Session } from "../../api/types";
import { AIKeyField } from "./AIKeyField";
import { useEvents } from "../events";
import { timecode, tokens, usd } from "../format";
import { Segmented } from "../motion/Segmented";
import { Link } from "../navigation";
import { startModel, useSettings } from "../settings";

const runStateText: Record<AIRun["state"], string> = {
  running: "보내는 중",
  completed: "완료",
  partial: "일부 완료",
  failed: "실패",
  cancelled: "중단됨",
};

const kindHelp = {
  explain: "고른 후보마다 장면이 무엇인지 짧은 설명을 받습니다. 요청이 적고 빠릅니다.",
  cut: "고른 후보마다 원인 분석 → 편집 구간 계획 → 검수 2회를 차례로 보내, 근거가 붙은 편집 구간(컷 계획)을 받습니다. 후보당 4~7회 요청합니다. 아직 읽은 범위(편집 구간 앞뒤 1분) 밖은 검색하지 않습니다.",
} as const;

const modeHelp = {
  candidates: "후보 목록에서 ‘AI 검토에 포함’으로 고른 후보마다 편집 구간과 앞뒤 채팅을 보내고, 그 장면이 무엇인지 설명을 받습니다.",
  all: "자동 탐지와 시청자 추천 후보를 모두 고르고, 비용 한도 안에서만 보냅니다.",
  range: "지정한 구간의 채팅을 그대로 보내고, 그 안에서 반응이 몰린 장면을 찾아 설명합니다.",
} as const;

const liveModeHelp = {
  candidates: "후보 목록에서 ‘AI 검토에 포함’으로 고른 확정 후보마다 편집 구간과 앞뒤 채팅을 보내고, 그 장면이 무엇인지 설명을 받습니다.",
  all: "확정된 자동 탐지 후보를 모두 고르고, 비용 한도 안에서만 보냅니다.",
  range: (endMs: number) => `지정한 구간의 채팅을 그대로 보냅니다. 끝 시각을 비우면 분석을 마친 ${timecode(endMs, true)}까지 보냅니다.`,
} as const;

const deliveryHelp = {
  single: "한 요청씩 보내고 바로 결과를 받습니다.",
  batch: "가격이 절반입니다. 결과는 보통 1시간 안, 늦어도 24시간 안에 옵니다.",
} as const;

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
    r.kind === "cut" ? `컷 계획 ${r.recommendations}개` : `설명·추천 ${r.recommendations}개`,
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
  /**
   * A LIVE capture's analysed point: a range ends by it (an empty end asks
   * the server for its own), and the help says a plan holds only the chat
   * stored when it was previewed.
   */
  liveEndMs?: number;
  /** Each change asks for a preview of the picked candidates (the candidate list's "AI 검토로 이동"). */
  previewAsk?: number;
  /** Back to the candidate list, where the explanations are shown. */
  onShowCandidates?: () => void;
}

/**
 * Opt-in AI review (CzzEdit's 선택형 AI): what to send is chosen first, the
 * provider is one line (its details fold out), and the plan is shown as is
 * with its most possible cost. Nothing is sent until the user confirms that
 * exact plan; the answers appear under the candidates they explain.
 */
export function AIPanel({ s, selection, liveEndMs, previewAsk = 0, onShowCandidates }: Props) {
  const queryClient = useQueryClient();
  const { subscribe } = useEvents();
  const providers = useQuery({ queryKey: ["aiProviders"], queryFn: api.aiProviders });
  const settings = useSettings();
  const runs = useQuery({ queryKey: ["aiRuns", s.id], queryFn: () => api.aiRuns(s.id) });
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [models, setModels] = useState<AIModel[]>([]);
  const [kind, setKind] = useState<"explain" | "cut">("explain");
  const [mode, setMode] = useState<"candidates" | "all" | "range">("candidates");
  // The cut analysis works on picked candidates only.
  useEffect(() => {
    if (kind === "cut") setMode("candidates");
  }, [kind]);
  const [from, setFrom] = useState("0:00:00");
  const live = liveEndMs !== undefined;
  const [to, setTo] = useState(live ? "" : timecode(s.durationMs, true));
  const [delivery, setDelivery] = useState<AIDelivery>("single");
  const [cap, setCap] = useState("");
  const [preview, setPreview] = useState<AIPreview | null>(null);
  const [counted, setCounted] = useState<AITokenCount | null>(null);
  const [runId, setRunId] = useState<string | null>(null);
  // The provider's details start open when it cannot send yet, and stay as the user leaves them.
  const [showDest, setShowDest] = useState<boolean | null>(null);

  const list = providers.data?.providers ?? [];
  // The settings screen's provider and model come first; the tab can change them for this review.
  const current = list.find((p) => p.id === (provider || settings.data?.aiProvider)) ?? list[0];
  const firstModel = startModel(settings.data, current);
  useEffect(() => {
    if (showDest === null && current) setShowDest(!current.configured);
  }, [showDest, current]);
  // A new provider starts from its default model and its own options.
  useEffect(() => {
    setModel(firstModel);
    setModels([]);
    if (!current?.canBatch) setDelivery("single");
    if (!current?.hasPrices) {
      // Without prices there is no cap, so no automatic selection either.
      setCap("");
      setMode((m) => (m === "all" ? "candidates" : m));
    }
  }, [current?.id, firstModel, current?.canBatch, current?.hasPrices]);
  // Any change to what would be sent drops the preview.
  const inputs = useMemo(
    () => JSON.stringify([current?.id, model, kind, mode, from, to, selection, delivery, cap]),
    [current?.id, model, kind, mode, from, to, selection, delivery, cap],
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

  const toEnd = live && to.trim() === "";
  const capValue = cap.trim() === "" ? undefined : Number(cap);
  const capInvalid = capValue !== undefined && (!Number.isFinite(capValue) || capValue <= 0);

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
        kind,
        mode,
        itemIds: mode === "candidates" ? selection : undefined,
        startMs: mode === "range" ? parseTime(from) : undefined,
        // An empty LIVE end is the server's analysed point when previewing, not this screen's last copy of it.
        endMs: mode === "range" && !toEnd ? parseTime(to) : undefined,
        toEnd: mode === "range" && toEnd ? true : undefined,
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

  const needsCap = mode === "all" && capValue === undefined;
  const canPreview =
    !!current && !makePreview.isPending && !!model.trim() && !capInvalid && !needsCap && (mode !== "candidates" || selection.length > 0);

  // The candidate list asked for a review of its picks: switch to them and preview at once.
  const [pendingAsk, setPendingAsk] = useState(0);
  useEffect(() => {
    if (previewAsk > 0) {
      setMode("candidates");
      setPendingAsk(previewAsk);
    }
  }, [previewAsk]);
  useEffect(() => {
    if (!pendingAsk || mode !== "candidates" || !current) return;
    setPendingAsk(0);
    if (current.configured && canPreview) makePreview.mutate();
    else setShowDest(true);
    // The preview reads the state of this render; the inputs above are the ones it sends.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pendingAsk, mode, current?.id]);

  const listModels = current?.canListModels && current.configured && (
    <button type="button" className="link-button" onClick={() => loadModels.mutate()} disabled={loadModels.isPending}>
      {models.length > 0 ? `모델 ${models.length}개 불러옴` : "쓸 수 있는 모델 불러오기"}
    </button>
  );
  const r = run.data;
  const failed = makePreview.error ?? start.error ?? cancel.error ?? loadModels.error ?? count.error;
  if (list.length === 0) return null;
  const plan = preview?.plan;
  const destOpen = showDest ?? true;
  return (
    <section className="panel-section" aria-labelledby="ai-title">
      <h2 id="ai-title" className="section-title">
        AI 검토
      </h2>
      <p className="field-help">
        고른 후보의 앞뒤 채팅을 AI에 보내 그 장면이 어떤 내용인지 설명을 받습니다. 보내기 전에 보낼 내용과 최대 비용을 보여 드리고,
        확인해야 보냅니다.
        {live && " 생방송은 수집 중에도 저절로 보내지 않으며, 미리보기를 만든 순간까지 저장된 채팅만 보내고 그 뒤에 받은 채팅은 넣지 않습니다."}
      </p>

      <div className="ai-step">
        <h3 className="ai-step-title">1. 무엇을 보낼지</h3>
        <div className="ai-mode">
          <Segmented
            name="ai-kind"
            legend="분석 종류"
            hideLegend
            value={kind}
            onChange={setKind}
            options={[
              { value: "explain" as const, label: "장면 설명" },
              { value: "cut" as const, label: "컷 분석" },
            ]}
          />
          <p className="field-help">{kindHelp[kind]}</p>
        </div>
        <div className="ai-mode">
          <Segmented
            name="ai-mode"
            legend="보낼 범위"
            hideLegend
            value={mode}
            onChange={setMode}
            options={[
              { value: "candidates" as const, label: `후보 목록에서 고른 ${selection.length}개` },
              ...(current?.hasPrices && kind === "explain" ? [{ value: "all" as const, label: "모든 후보 자동 선택" }] : []),
              ...(kind === "explain" ? [{ value: "range" as const, label: "채팅 구간 직접 지정" }] : []),
            ]}
          />
          <p className="field-help">
            {kind === "cut"
              ? "고른 후보마다 편집 구간 앞뒤 1분의 발언(전사가 있을 때)과 채팅을 읽고 시작합니다."
              : !live
                ? modeHelp[mode]
                : mode === "range"
                  ? liveModeHelp.range(liveEndMs)
                  : liveModeHelp[mode]}
          </p>
          {mode === "range" && (
            <span className="ai-range">
              <input type="text" aria-label="시작 시각" value={from} onChange={(e) => setFrom(e.target.value)} /> ~{" "}
              <input
                type="text"
                aria-label="끝 시각"
                value={to}
                onChange={(e) => setTo(e.target.value)}
                placeholder={live ? "분석한 위치" : undefined}
              />
            </span>
          )}
          {mode === "candidates" && selection.length === 0 && onShowCandidates && (
            <p className="field-help">
              아직 고른 후보가 없습니다.{" "}
              <button type="button" className="link-button" onClick={onShowCandidates}>
                후보 탭에서 ‘AI 검토에 포함’을 체크하세요
              </button>
              .
            </p>
          )}
        </div>
      </div>

      <div className="ai-step">
        <h3 className="ai-step-title">2. 어디로 보낼지</h3>
        <details className="ai-dest" open={destOpen} onToggle={(e) => setShowDest(e.currentTarget.open)}>
          <summary>
            <span className="ai-dest-state" data-ready={current?.configured || undefined}>
              {current?.name}
              {model.trim() ? ` · ${model.trim()}` : ""}
            </span>
            <span className="ai-dest-ready">{current?.configured ? (current.selfAuth ? "준비됨" : "키 있음") : current?.selfAuth ? "준비 안 됨" : "키 없음"}</span>
            <span className="ai-dest-toggle">{destOpen ? "접기" : "바꾸기"}</span>
          </summary>
          <div className="ai-dest-body">
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
              {current?.selfAuth && (
                <div className="field">
                  <span className="field-label">로그인</span>
                  <span className="ai-auth" data-ready={current.configured || undefined}>
                    {current.configured ? "자체 로그인으로 보냄 (계정과 구독은 실행할 때 확인)" : current.notReady}
                  </span>
                </div>
              )}
            </div>
            {current?.selfAuth && current.authHelp && <p className="field-help">{current.authHelp}</p>}
            {current?.endpoint && (
              <p className="field-help ai-endpoint">
                보낼 주소 <span className="ai-endpoint-url">{current.endpoint}</span>. 주소는 <Link href="/settings">설정</Link>에서
                바꿉니다.
              </p>
            )}
            {current && !current.selfAuth && (
              <AIKeyField
                key={current.id}
                provider={current}
                extra={listModels}
                onChanged={() => void queryClient.invalidateQueries({ queryKey: ["aiRuns", s.id] })}
              />
            )}
            {current?.selfAuth && listModels && <p className="actions">{listModels}</p>}
            {(current?.canBatch || current?.hasPrices) && (
              <div className="ai-grid">
                {current?.canBatch && (
                  <div className="ai-mode">
                    <Segmented
                      name="ai-delivery"
                      legend="전송 방식"
                      value={delivery}
                      onChange={setDelivery}
                      options={[
                        { value: "single" as const, label: "바로 보내기" },
                        { value: "batch" as const, label: "배치로 보내기" },
                      ]}
                    />
                    <p className="field-help">{deliveryHelp[delivery]}</p>
                  </div>
                )}
                {current?.hasPrices && (
                  <label className="field ai-cap">
                    <span className="field-label">비용 한도 (미국 달러{mode === "all" ? ", 필수" : ", 비우면 없음"})</span>
                    <input type="text" inputMode="decimal" value={cap} onChange={(e) => setCap(e.target.value)} placeholder="예: 2" />
                  </label>
                )}
              </div>
            )}
            <p className="field-help">
              기본 제공자·모델과 이 컴퓨터에 저장할 키는 <Link href="/settings">설정</Link>에서 정합니다. API 키는 앱이 실행되는 동안
              메모리에 두고, ‘이 컴퓨터에도 저장’을 고른 키만 데이터 폴더의 .env에 남깁니다.
            </p>
          </div>
        </details>
      </div>

      <div className="ai-step">
        <h3 className="ai-step-title">3. 확인하고 보내기</h3>
        <p className="actions">
          <button type="button" className="button button-quiet" onClick={() => makePreview.mutate()} disabled={!canPreview}>
            보낼 내용 미리보기
          </button>
          {capInvalid && <span className="field-help">비용 한도는 0보다 큰 숫자로 입력하세요.</span>}
          {needsCap && <span className="field-help">모든 후보를 보낼 때는 비용 한도를 정하세요.</span>}
        </p>
        {plan && preview && (
          <div className="ai-preview" role="status" data-kind={plan.kind ?? "explain"}>
            {preview.cut ? (
              <p className="ai-preview-sum">
                후보 {preview.cut.inputs.length}개, 후보당 {preview.cut.plannedPerCandidate}~{preview.cut.requestsPerCandidate}회(최대 요청{" "}
                {plan.request_count}회), 읽는 자료 {preview.cut.hasTranscript ? `발언 ${preview.cut.utteranceCount}건과 ` : ""}채팅{" "}
                {preview.cut.chatCount}건, 입력 추정 약 {tokens(plan.planned_input_token_estimate)}토큰.{" "}
                {preview.cut.hasTranscript ? "" : "이 작업에는 전사가 없어 채팅만으로 분석합니다. "}
                {preview.cut.searchExecuted ? "" : "읽은 범위 밖은 검색하지 않습니다. "}10분 뒤 만료됩니다.
              </p>
            ) : (
              <p className="ai-preview-sum">
                {plan.ranges.length > 0 && <>구간 {plan.ranges.length}개, </>}
                요청 {plan.request_count}회, 입력 추정 약 {tokens(plan.planned_input_token_estimate)}토큰, 출력 상한{" "}
                {tokens(plan.output_token_limit)}토큰
                {plan.delivery === "batch" ? ". 한 번에 배치로 보냅니다. " : `, 재시도 포함 최대 ${plan.request_attempt_limit}회. `}
                {plan.omitted.length > 0 &&
                  `보내지 않는 부분 ${plan.omitted.length}개(${[...new Set(plan.omitted.map((o) => omittedText[o.reason] ?? o.reason))].join(", ")}). `}
                10분 뒤 만료됩니다.
              </p>
            )}
            {plan.price ? (
              <>
                <p className="ai-cost">
                  예상 비용 최대 {usd(plan.cost_upper_usd ?? 0)}
                  {plan.cost_cap_usd ? ` (한도 ${usd(plan.cost_cap_usd)})` : ""}. {plan.price.as_of} 공시 가격 기준
                  {plan.delivery === "batch" ? ", 배치 50% 할인 적용" : ""}.
                </p>
                {plan.cost_cap_usd ? <CapMeter used={plan.cost_upper_usd ?? 0} cap={plan.cost_cap_usd} /> : null}
              </>
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
              <summary>{preview.cut ? "보낼 내용 전체 (첫 단계 지시문, 후보별 발언·채팅)" : "보낼 내용 전체 (지시문, 구간, 채팅)"}</summary>
              <pre className="ai-plan">
                {JSON.stringify(
                  preview.cut
                    ? { instructions: plan.prompt, candidates: preview.cut.inputs }
                    : { instructions: plan.prompt, ranges: plan.ranges, chunks: plan.chunks, omitted: plan.omitted },
                  null,
                  2,
                )}
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
      </div>
      {r && (
        <p className="ai-status" role="status" data-state={r.state}>
          <span className={r.state === "running" ? "shimmer" : undefined}>
            AI {r.state === "running" && r.delivery === "batch" ? "배치" : runStateText[r.state]}
          </span>
          : {runSummary(r)}
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
      {r && r.state !== "running" && r.recommendations > 0 && (
        <p className="ai-done" role="status">
          {r.kind === "cut"
            ? "컷 계획은 후보 목록의 각 후보 아래 ‘AI 컷 분석’으로 들어갔습니다. 검수에 걸린 계획은 그 이유와 함께 표시됩니다."
            : "설명은 후보 목록의 각 후보 아래 ‘AI 설명’으로, 새로 찾은 장면은 ‘AI 추천’ 행으로 들어갔습니다."}
          {onShowCandidates && (
            <>
              {" "}
              <button type="button" className="link-button" onClick={onShowCandidates}>
                후보 보기
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

/** How much of the cost cap the plan can use at most; it turns to the alert color near the cap. */
function CapMeter({ used, cap }: { used: number; cap: number }) {
  const share = Math.min(1, used / cap);
  return (
    <div className="cap" data-near={share >= 0.85 || undefined}>
      <div className="cap-meter" role="meter" aria-label="비용 한도 사용" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(share * 100)}>
        <span className="cap-fill" style={{ "--p": share } as CSSProperties} />
      </div>
      <span className="cap-text">최대로 써도 한도의 {Math.round(share * 100)}%</span>
    </div>
  );
}

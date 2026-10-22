import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Session } from "../../api/types";
import { ConfirmAction } from "../confirm/ConfirmAction";
import { Link } from "../navigation";
import { taskModel, useSettings } from "../settings";
import { RunStatus } from "./RunStatus";

interface Props {
  s: Session;
  /** The session has candidates to explain. */
  hasCandidates: boolean;
  /** A job runs (one at a time), so the review cannot start now. */
  busy: boolean;
}

/**
 * The AI review of a session, now that it needs no tab of its own: whether
 * it runs by itself (일반 설정) or, when it does not, the way to ask for this
 * session's scene explanations (asked once more with what it sends), and
 * the runs going on with their progress and a way to stop them. The
 * results appear under the candidates they explain.
 */
export function AIReviewBar({ s, hasCandidates, busy }: Props) {
  const settings = useSettings();
  const providers = useQuery({ queryKey: ["aiProviders"], queryFn: api.aiProviders });
  const runs = useQuery({ queryKey: ["aiRuns", s.id], queryFn: () => api.aiRuns(s.id) });
  // The job's progress arrives as "job" events (useJob); asking for it here could answer after it ended.
  const review = useMutation({ mutationFn: () => api.startAIReview(s.id) });
  const list = providers.data?.providers ?? [];
  const provider = list.find((p) => p.id === settings.data?.aiProvider) ?? list[0];
  const running = (runs.data?.runs ?? []).filter((r) => r.state === "running");
  const longForm = s.durationMs >= 3_600_000;
  // The sample's synthetic chat goes only when asked, never by itself.
  const auto = (settings.data?.autoAI ?? false) && !s.isDemo;
  const ready = provider?.configured ?? false;
  const limits = settings.data
    ? `요청 ${settings.data.autoAIMaxRequests}회${provider?.hasPrices ? `, 비용 ${settings.data.autoAICostCapUsd}달러` : ""}`
    : "";
  return (
    <div className="ai-bar" data-auto={auto || undefined}>
      <p className="ai-bar-state">
        {auto ? (
          <>
            <strong>AI 검토 자동 실행 중.</strong> 새 편집 후보는 {provider?.name ?? "기본 AI 제공자"}(
            {taskModel(settings.data, provider, "explain")})로 장면 설명을 받습니다.
          </>
        ) : s.isDemo ? (
          <>샘플은 저절로 AI로 보내지 않습니다. 직접 받아 흐름을 볼 수 있습니다.</>
        ) : (
          <>AI 장면 설명은 자동으로 받지 않습니다.</>
        )}{" "}
        <Link href="/settings">일반 설정</Link>
      </p>
      {!auto && hasCandidates && (
        <div className="actions">
          {ready ? (
            <ConfirmAction
              label="AI 장면 설명 받기"
              question={`후보 앞뒤의 채팅(가명)과 대본을 ${provider!.name}로 보내 후보마다 장면 설명을 받습니다. 이 작업의 한도는 ${limits}입니다.`}
              keepLabel="보내지 않기"
              onConfirm={() => review.mutate()}
              disabled={busy || review.isPending}
              className="button button-quiet"
            />
          ) : (
            <span className="field-help">
              {provider ? `${provider.name}을(를) 쓸 준비가 되지 않았습니다. ` : ""}
              <Link href="/settings">설정 &gt; AI 검토</Link>에서 키나 로그인을 확인하세요.
            </span>
          )}
        </div>
      )}
      {running.map((r) => (
        <RunStatus key={r.id} r={r} longForm={longForm} />
      ))}
      {review.isError && (
        <p className="error" role="alert">
          {review.error.message}
        </p>
      )}
    </div>
  );
}

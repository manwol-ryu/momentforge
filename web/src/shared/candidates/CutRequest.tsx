import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import { ConfirmAction } from "../confirm/ConfirmAction";
import { Link } from "../navigation";
import { taskModel, useSettings } from "../settings";

interface Props {
  sessionId: string;
  /** The candidate's timeline item ID, and how the list names it ("후보 3"). */
  itemId: string;
  name: string;
  /** A result box's "다시 분석": the plan the new analysis continues. */
  planId?: string;
  /** The button: "AI 컷 분석" under a candidate, "다시 분석" in a result box. */
  label: string;
  className?: string;
}

/**
 * Starts one candidate's cut analysis right away (no preview: the user
 * presses it for that candidate). With the automatic review off it asks
 * once more, saying what goes out; with it on, the user agreed already.
 * While the analysis runs it says so; the result box appears under the
 * candidate when it ends.
 */
export function CutRequest({ sessionId, itemId, name, planId, label, className = "link-button" }: Props) {
  const queryClient = useQueryClient();
  const settings = useSettings();
  const providers = useQuery({ queryKey: ["aiProviders"], queryFn: api.aiProviders });
  const [runId, setRunId] = useState<string | null>(null);
  const start = useMutation({
    mutationFn: () => api.startCut(sessionId, itemId, planId),
    onSuccess: (r) => {
      setRunId(r.id);
      void queryClient.invalidateQueries({ queryKey: ["aiRuns", sessionId] });
    },
  });
  const run = useQuery({ queryKey: ["aiRun", runId], queryFn: () => api.aiRun(runId!), enabled: !!runId });
  const list = providers.data?.providers ?? [];
  const provider = list.find((p) => p.id === settings.data?.aiProvider) ?? list[0];
  if (run.data?.state === "running") {
    return (
      <span className="cut-running" role="status">
        <span className="shimmer">AI 컷 분석 중</span> ({run.data.completedRequests}/{run.data.requestCount}회)
      </span>
    );
  }
  if (!provider?.configured) {
    return (
      <span className="field-help">
        컷 분석은 <Link href="/settings">설정 &gt; AI 검토</Link>에서 AI 제공자를 준비하면 쓸 수 있습니다.
      </span>
    );
  }
  const model = taskModel(settings.data, provider, "cut");
  const failed = start.error ?? (run.data?.state === "failed" ? new Error(run.data.errorMessage ?? "컷 분석을 완료하지 못했습니다.") : null);
  return (
    <>
      {settings.data?.autoAI ? (
        <button type="button" className={className} onClick={() => start.mutate()} disabled={start.isPending}>
          {label}
        </button>
      ) : (
        <ConfirmAction
          label={label}
          question={`${name}의 편집 구간 앞뒤 1분 채팅(가명)과 대본을 ${provider.name}(${model})로 보내 컷 분석을 받습니다. 요청은 4~11회입니다.`}
          keepLabel="보내지 않기"
          onConfirm={() => start.mutate()}
          disabled={start.isPending}
          className={className}
        />
      )}
      {failed && (
        <span className="error" role="alert">
          {" "}
          {failed.message}
        </span>
      )}
    </>
  );
}

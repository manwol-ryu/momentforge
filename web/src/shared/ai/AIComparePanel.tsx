import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { AIRecommendation, AIRun, Session } from "../../api/types";
import { timecode, tokens, usd } from "../format";

const priorityText: Record<AIRecommendation["priority"], string> = { high: "높음", medium: "보통", low: "낮음" };

const deliveryText = { single: "바로", batch: "배치" } as const;

/**
 * Two finished AI reviews of one video side by side, like two tracks of an
 * editing timeline: moments both recommended (overlapping in time) are
 * filled, moments only one recommended are outlined.
 */
export function AIComparePanel({ s }: { s: Session }) {
  const providers = useQuery({ queryKey: ["aiProviders"], queryFn: api.aiProviders });
  const runs = useQuery({ queryKey: ["aiRuns", s.id], queryFn: () => api.aiRuns(s.id) });
  const done = (runs.data?.runs ?? []).filter((r) => r.state === "completed" || r.state === "partial");
  const [a, setA] = useState("");
  const [b, setB] = useState("");
  // Default: the newest review against the newest one by another provider (or the next newest).
  useEffect(() => {
    const first = done[0];
    if (!first || done.length < 2 || (a && b)) return;
    const other = done.find((r) => r.provider !== first.provider) ?? done[1];
    setA(first.id);
    setB(other?.id ?? "");
  }, [done, a, b]);
  const cmp = useQuery({
    queryKey: ["aiCompare", a, b],
    queryFn: () => api.aiCompare(a, b),
    enabled: !!a && !!b && a !== b,
  });
  if (done.length < 2) return null;

  const name = (id: string) => providers.data?.providers.find((p) => p.id === id)?.name ?? id;
  const label = (r: AIRun) =>
    `${name(r.provider)} ${r.model}, ${new Date(r.createdAt).toLocaleString("ko-KR", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}, 추천 ${r.recommendations}개`;
  const c = cmp.data;
  const duration = Math.max(1, s.durationMs);
  const track = (recs: { rec: AIRecommendation; matched: boolean }[], which: string) => (
    <div className="compare-track" aria-hidden="true">
      <span className="compare-track-label">{which}</span>
      <div className="compare-track-bar">
        {recs.map(({ rec, matched }) => (
          <span
            key={rec.id}
            className="compare-segment"
            data-matched={matched || undefined}
            style={{ left: `${(rec.startMs / duration) * 100}%`, width: `max(4px, ${((rec.endMs - rec.startMs) / duration) * 100}%)` }}
            title={`${timecode(rec.startMs, true)} ${rec.title}`}
          />
        ))}
      </div>
    </div>
  );
  const cell = (rec?: AIRecommendation) =>
    rec ? (
      <>
        <span className="timecode">
          {timecode(rec.startMs, true)}–{timecode(rec.endMs, true)}
        </span>{" "}
        {rec.title} <span className="file-size">중요도 {priorityText[rec.priority]}</span>
      </>
    ) : (
      <span className="empty-cell">추천 없음</span>
    );
  const rows = c
    ? [
        ...c.matched.map((m) => ({ at: m.a.startMs, a: m.a, b: m.b })),
        ...c.onlyA.map((x) => ({ at: x.startMs, a: x, b: undefined })),
        ...c.onlyB.map((x) => ({ at: x.startMs, a: undefined, b: x })),
      ].sort((x, y) => x.at - y.at)
    : [];

  return (
    <section className="panel-section" aria-labelledby="ai-compare-title">
      <h2 id="ai-compare-title" className="section-title">
        AI 결과 비교
      </h2>
      <div className="ai-grid">
        <label className="field">
          <span className="field-label">A 검토</span>
          <select value={a} onChange={(e) => setA(e.target.value)}>
            {done.map((r) => (
              <option key={r.id} value={r.id}>
                {label(r)}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span className="field-label">B 검토</span>
          <select value={b} onChange={(e) => setB(e.target.value)}>
            {done.map((r) => (
              <option key={r.id} value={r.id}>
                {label(r)}
              </option>
            ))}
          </select>
        </label>
      </div>
      {a === b && <p className="field-help">서로 다른 검토 두 개를 고르세요.</p>}
      {c && (
        <>
          <p className="comments-summary compare-summary" role="status">
            겹치는 추천 {c.matched.length}개, A에만 {c.onlyA.length}개, B에만 {c.onlyB.length}개.{" "}
            {c.sameInput ? "두 검토는 같은 채팅을 보냈습니다." : "두 검토가 보낸 채팅 범위가 달라 차이가 범위 때문일 수 있습니다."}
          </p>
          <div className="compare-tracks">
            {track(
              [...c.matched.map((m) => ({ rec: m.a, matched: true })), ...c.onlyA.map((rec) => ({ rec, matched: false }))],
              "A",
            )}
            {track(
              [...c.matched.map((m) => ({ rec: m.b, matched: true })), ...c.onlyB.map((rec) => ({ rec, matched: false }))],
              "B",
            )}
          </div>
          <table className="compare-table">
            <caption className="visually-hidden">A와 B 검토의 추천을 시간 순으로 맞춘 표</caption>
            <thead>
              <tr>
                <th scope="col">A: {name(c.a.provider)}</th>
                <th scope="col">B: {name(c.b.provider)}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i} data-kind={row.a && row.b ? "both" : row.a ? "a" : "b"}>
                  <td>{cell(row.a)}</td>
                  <td>{cell(row.b)}</td>
                </tr>
              ))}
            </tbody>
            <tfoot>
              <tr>
                <td>{runCost(c.a)}</td>
                <td>{runCost(c.b)}</td>
              </tr>
            </tfoot>
          </table>
        </>
      )}
      {cmp.error && (
        <p className="error" role="alert">
          {cmp.error.message}
        </p>
      )}
    </section>
  );
}

function runCost(r: AIRun): string {
  const parts = [`${r.model} (${deliveryText[r.delivery]})`, `입력 ${tokens(r.inputTokens)}토큰`];
  if (r.cacheReadTokens > 0) parts.push(`캐시 적중 ${tokens(r.cacheReadTokens)}`);
  parts.push(`출력 ${tokens(r.outputTokens)}토큰`);
  if (r.costUsd != null) parts.push(`비용 ${usd(r.costUsd)}`);
  return parts.join(", ");
}

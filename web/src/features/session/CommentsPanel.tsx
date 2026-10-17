import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { Session } from "../../api/types";

const issueText: Record<string, string> = {
  page_budget: "페이지 한도(100쪽)에 닿음",
  reply_incomplete: "답글 일부를 못 읽음",
  reply_stalled: "답글 목록이 진행되지 않음",
  manual_paste: "직접 붙여넣은 댓글",
};

const reasonText: Record<string, string> = {
  needs_review: "시계나 상대 시각일 수 있음",
  invalid_time: "영상 길이를 벗어남",
  invalid_range: "범위가 맞지 않음",
};

/**
 * Viewer recommendations from the video's comments: read them from the
 * platform, or paste comments with times when that is not possible.
 */
export function CommentsPanel({ s, busy }: { s: Session; busy: boolean }) {
  const comments = useQuery({ queryKey: ["comments", s.id], queryFn: () => api.comments(s.id) });
  const [text, setText] = useState("");
  const read = useMutation({ mutationFn: () => api.startComments({ sessionId: s.id }) });
  const paste = useMutation({
    mutationFn: () => api.startComments({ sessionId: s.id, text }),
    onSuccess: () => setText(""),
  });
  const c = comments.data;
  const failed = read.error ?? paste.error;
  return (
    <section className="panel-section" aria-labelledby="comments-title">
      <h2 id="comments-title" className="section-title">
        시청자 추천 (영상 댓글)
      </h2>
      {c ? (
        <p className="comments-summary">
          {c.source === "website" ? `영상 댓글 ${c.commentCount.toLocaleString("ko-KR")}개` : "붙여넣은 댓글"}에서 시각{" "}
          {c.recommendations.length}개를 찾았습니다.
          {c.state === "partial" && c.source === "website" && (
            <> 일부만 읽었습니다({c.issues.map((i) => issueText[i] ?? i).join(", ")}).</>
          )}
        </p>
      ) : (
        <p className="empty">
          {comments.isLoading ? "확인하는 중" : "아직 영상 댓글을 읽지 않았습니다. 댓글에 적힌 시각은 자동 후보와 따로 표시됩니다."}
        </p>
      )}
      {s.canReadComments && (
        <p className="actions">
          <button type="button" className="button button-quiet" onClick={() => read.mutate()} disabled={busy || read.isPending}>
            {c?.source === "website" ? "영상 댓글 다시 읽기" : "영상 댓글 읽기"}
          </button>
        </p>
      )}
      <details className="panel">
        <summary>시각이 적힌 댓글 직접 붙여넣기</summary>
        <label className="field">
          <span className="field-label">댓글 원문 (12,000자 이하)</span>
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={5}
            maxLength={12000}
            placeholder={"1:23 웃긴 장면\n12:00~13:30 하이라이트"}
          />
        </label>
        <p className="actions">
          <button
            type="button"
            className="button button-quiet"
            onClick={() => paste.mutate()}
            disabled={busy || paste.isPending || !text.trim()}
          >
            붙여넣은 추천 저장
          </button>
        </p>
      </details>
      {c && c.needsReview.length > 0 && (
        <details className="panel">
          <summary>확인이 필요한 시각 {c.needsReview.length}개</summary>
          <ul className="review-list">
            {c.needsReview.map((r) => (
              <li key={r.commentId}>
                <span className="source-note">{r.text}</span>{" "}
                <span className="file-size">{[...new Set(r.reasons)].map((x) => reasonText[x] ?? x).join(", ")}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
      {failed && (
        <p className="error" role="alert">
          {failed.message}
        </p>
      )}
    </section>
  );
}

import { memo, useEffect, useRef } from "react";
import type { Chat } from "../../api/types";
import { timecode } from "../format";
import { authorTag, ChatText } from "./ChatText";

/** The last message at or before a time (-1: none yet). Chats are in time order. */
export function currentChat(chats: readonly Chat[], ms: number): number {
  let lo = 0;
  let hi = chats.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (chats[mid]!.offsetMs <= ms) lo = mid + 1;
    else hi = mid;
  }
  return lo - 1;
}

interface Props {
  chats: (Chat & { isEvidence?: boolean })[];
  /** Highlight and keep in view the last message at or before this time. */
  currentMs?: number;
  follow?: boolean;
  longForm?: boolean;
  label: string;
  /** Pressing a row moves the video (or the playhead) to that message's time. */
  onPick?: (ms: number) => void;
}

/**
 * A chat log with timecodes: one compact row per message, the author as a
 * short tag, the platform's emote codes as chips, evidence messages marked.
 * With onPick every row is a button that jumps the video to that message.
 * Memoized: at the playhead it re-renders when the current message changes,
 * not on every time update.
 */
export const ChatList = memo(function ChatList({ chats, currentMs, follow = false, longForm = false, label, onPick }: Props) {
  const list = useRef<HTMLOListElement>(null);
  const current = currentMs === undefined ? -1 : currentChat(chats, currentMs);

  useEffect(() => {
    if (!follow || current < 0) return;
    const box = list.current;
    const item = box?.children[current] as HTMLElement | undefined;
    if (!item || !box) return;
    // Measured from the list itself: offsetTop would count from the nearest positioned ancestor, not the list.
    const top = item.getBoundingClientRect().top - box.getBoundingClientRect().top + box.scrollTop;
    box.scrollTop = top - (box.clientHeight - item.offsetHeight) / 2;
  }, [follow, current, chats]);

  if (chats.length === 0) return <p className="empty">이 구간에 저장된 채팅이 없습니다.</p>;
  return (
    <ol ref={list} className="chatlog" aria-label={label} tabIndex={0} data-pickable={onPick ? true : undefined}>
      {chats.map((c, i) => {
        const tag = authorTag(c.author);
        const body = (
          <>
            <span className="chat-time timecode">{timecode(c.offsetMs, longForm)}</span>
            <span className="chat-author" data-tone={tag.tone} title={c.author || "작성자를 알 수 없음"}>
              {c.kind === "system" ? "알림" : tag.short}
            </span>
            {c.kind === "donation" && (
              <span className="chat-kind" data-kind="donation" title="후원">
                💰 {(c.amount ?? 0).toLocaleString("ko-KR")}원
              </span>
            )}
            {c.kind === "subscription" && (
              <span className="chat-kind" data-kind="subscription" title="구독">
                ⭐ 구독 {c.amount ?? 0}개월
              </span>
            )}
            <span className="chat-text">
              {c.isEvidence && (
                <span className="chat-mark" aria-label="판정 근거">
                  *
                </span>
              )}
              <ChatText text={c.text} />
            </span>
          </>
        );
        return (
          <li key={c.id} data-current={i === current || undefined} data-evidence={c.isEvidence || undefined} data-kind={c.kind} data-hidden={c.hidden || undefined}>
            {onPick ? (
              <button type="button" className="chat-row" title="이 시각으로 이동" onClick={() => onPick(c.offsetMs)}>
                {body}
              </button>
            ) : (
              <div className="chat-row">{body}</div>
            )}
          </li>
        );
      })}
    </ol>
  );
});

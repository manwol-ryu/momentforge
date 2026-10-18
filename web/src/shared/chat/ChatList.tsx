import { useEffect, useRef } from "react";
import type { Chat } from "../../api/types";
import { timecode } from "../format";
import { authorTag, ChatText } from "./ChatText";

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
 */
export function ChatList({ chats, currentMs, follow = false, longForm = false, label, onPick }: Props) {
  const list = useRef<HTMLOListElement>(null);
  let current = -1;
  if (currentMs !== undefined) {
    for (let i = 0; i < chats.length && chats[i]!.offsetMs <= currentMs; i++) current = i;
  }

  useEffect(() => {
    if (!follow || current < 0) return;
    const item = list.current?.children[current] as HTMLElement | undefined;
    const box = list.current;
    if (item && box) box.scrollTop = item.offsetTop - box.clientHeight / 2;
  }, [follow, current]);

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
}

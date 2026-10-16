import { useEffect, useRef } from "react";
import type { Chat } from "../../api/types";
import { timecode } from "../format";

interface Props {
  chats: (Chat & { isEvidence?: boolean })[];
  /** Highlight and keep in view the last message at or before this time. */
  currentMs?: number;
  follow?: boolean;
  longForm?: boolean;
  label: string;
}

/** A scrollable chat log with timecodes; evidence messages are marked. */
export function ChatList({ chats, currentMs, follow = false, longForm = false, label }: Props) {
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
    <ol ref={list} className="chatlog" aria-label={label} tabIndex={0}>
      {chats.map((c, i) => (
        <li key={c.id} data-current={i === current || undefined} data-evidence={c.isEvidence || undefined}>
          <span className="chat-time timecode">{timecode(c.offsetMs, longForm)}</span>
          {c.isEvidence && <span className="chat-mark" aria-label="판정 근거">*</span>}
          <span className="chat-author">{c.author || "알 수 없음"}</span>
          <span className="chat-text">{c.text}</span>
        </li>
      ))}
    </ol>
  );
}

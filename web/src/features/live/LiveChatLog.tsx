import { useLayoutEffect, useRef, useState } from "react";
import type { LiveChat } from "../../api/types";
import { authorTag, ChatText } from "../../shared/chat/ChatText";
import { timecode } from "../../shared/format";

interface Props {
  chats: LiveChat[];
  longForm: boolean;
}

/**
 * The newest chat, kept scrolled to the bottom. Scrolling up to read holds
 * the list still and counts what arrived since; "최신 채팅으로" resumes.
 */
export function LiveChatLog({ chats, longForm }: Props) {
  const box = useRef<HTMLOListElement>(null);
  // Seq of the last message shown when the reader scrolled away; null while following.
  const [heldAt, setHeldAt] = useState<number | null>(null);
  const shown = heldAt === null ? chats : chats.filter((c) => c.seq <= heldAt);
  // Messages that were already there when the log first filled do not animate; later ones push in.
  const firstSeq = useRef<number | null>(null);
  if (firstSeq.current === null && chats.length > 0) firstSeq.current = chats[chats.length - 1]?.seq ?? 0;
  const unseen = chats.length - shown.length;

  useLayoutEffect(() => {
    const el = box.current;
    if (el && heldAt === null) el.scrollTop = el.scrollHeight;
  }, [chats, heldAt]);

  const onScroll = () => {
    const el = box.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (!atBottom && heldAt === null) setHeldAt(chats[chats.length - 1]?.seq ?? 0);
    else if (atBottom && heldAt !== null && unseen === 0) setHeldAt(null);
  };

  return (
    <div className="live-log">
      {shown.length === 0 ? (
        <p className="empty">아직 받은 채팅이 없습니다. 연결되면 여기에 바로 보입니다.</p>
      ) : (
        <ol ref={box} className="chatlog chatlog-live" aria-label="실시간 채팅" aria-live="off" tabIndex={0} onScroll={onScroll}>
          {shown.map((c) => {
            const tag = authorTag(c.author);
            return (
              <li key={c.seq} data-late={c.late || undefined} data-new={c.seq > (firstSeq.current ?? Infinity) || undefined}>
                <div className="chat-row">
                  <span className="chat-time timecode">{timecode(c.offsetMs, longForm)}</span>
                  <span className="chat-author" data-tone={tag.tone} title={c.author || "작성자를 알 수 없음"}>
                    {tag.short}
                  </span>
                  <span className="chat-text">
                    <ChatText text={c.text} />
                  </span>
                  {c.late && <span className="chat-late">늦게 받음</span>}
                </div>
              </li>
            );
          })}
        </ol>
      )}
      {heldAt !== null && (
        <button type="button" className="link-button live-follow" onClick={() => setHeldAt(null)}>
          {unseen > 0 ? `새 채팅 ${unseen.toLocaleString("ko-KR")}건, 최신 채팅으로` : "최신 채팅으로"}
        </button>
      )}
    </div>
  );
}

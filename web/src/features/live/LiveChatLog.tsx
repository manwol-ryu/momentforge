import { useLayoutEffect, useRef, useState } from "react";
import type { LiveChat } from "../../api/types";
import { authorTag, ChatText } from "../../shared/chat/ChatText";
import { timecode } from "../../shared/format";

interface Props {
  chats: LiveChat[];
  /** Messages taken in so far (the list keeps only the newest), to count what arrived while reading. */
  received: number;
  longForm: boolean;
  /** Why the chat could not be loaded, with a way to try again; shown in place of the log while it is empty. */
  error?: Error | null;
  onRetry?: () => void;
}

/**
 * The newest chat, kept scrolled to the bottom. Scrolling up to read holds
 * the list as it was (newer messages wait instead of pushing older ones
 * out while they are read) and counts what arrived since; "최신 채팅으로"
 * resumes.
 */
export function LiveChatLog({ chats, received, longForm, error, onRetry }: Props) {
  const box = useRef<HTMLOListElement>(null);
  // The list as it was when the reader scrolled away, and the count then; null while following.
  const [held, setHeld] = useState<{ chats: LiveChat[]; at: number } | null>(null);
  const shown = held ? held.chats : chats;
  // Messages that were already there when the log first filled do not animate; later ones push in.
  const firstSeq = useRef<number | null>(null);
  if (firstSeq.current === null && chats.length > 0) firstSeq.current = chats[chats.length - 1]?.seq ?? 0;
  const unseen = held ? received - held.at : 0;

  useLayoutEffect(() => {
    const el = box.current;
    if (el && !held) el.scrollTop = el.scrollHeight;
  }, [chats, held]);

  const onScroll = () => {
    const el = box.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (!atBottom && !held) setHeld({ chats, at: received });
    else if (atBottom && held && unseen === 0) setHeld(null);
  };

  return (
    <div className="live-log">
      {shown.length === 0 ? (
        error ? (
          <div className="empty" role="alert">
            <p className="error">채팅을 불러오지 못했습니다. {error.message}</p>
            {onRetry && (
              <p className="actions">
                <button type="button" className="button button-quiet" onClick={onRetry}>
                  다시 불러오기
                </button>
              </p>
            )}
          </div>
        ) : (
          <p className="empty">아직 받은 채팅이 없습니다. 연결되면 여기에 바로 보입니다.</p>
        )
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
      {held && (
        <button type="button" className="link-button live-follow" onClick={() => setHeld(null)}>
          {unseen > 0 ? `새 채팅 ${unseen.toLocaleString("ko-KR")}건, 최신 채팅으로` : "최신 채팅으로"}
        </button>
      )}
    </div>
  );
}

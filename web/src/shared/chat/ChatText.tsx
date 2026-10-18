import type { ReactNode } from "react";

const EMOTE = /\{:([A-Za-z0-9_]+):\}/g;

/**
 * Chat text with the platform's emote codes ("{:name:}", which the page
 * cannot draw without fetching the picture) shown as small chips named after
 * the emote, so a row of emotes no longer wraps into lines of raw code.
 */
export function ChatText({ text }: { text: string }) {
  const parts: ReactNode[] = [];
  let last = 0;
  let n = 0;
  EMOTE.lastIndex = 0;
  for (let m = EMOTE.exec(text); m; m = EMOTE.exec(text)) {
    if (m.index > last) parts.push(text.slice(last, m.index));
    parts.push(
      <span key={n++} className="chat-emote" title={`이모티콘 ${m[1]}`}>
        {m[1]}
      </span>,
    );
    last = m.index + m[0].length;
  }
  if (parts.length === 0) return <>{text}</>;
  if (last < text.length) parts.push(text.slice(last));
  return <>{parts}</>;
}

/** The pseudonym's short form: its first four characters (the full key is in the title). */
export function authorTag(author: string): { short: string; tone: number } {
  if (!author) return { short: "익명", tone: 0 };
  const tone = (parseInt(author.slice(0, 2), 16) || 0) % 6;
  return { short: author.slice(0, 4), tone };
}

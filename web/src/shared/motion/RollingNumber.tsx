import type { CSSProperties } from "react";

/**
 * A count whose digits roll into place when it changes (each digit is a
 * strip of 0–9 moved by a spring). The plain number stays in the text, see-
 * through, so it keeps the width and is what screen readers, copying and
 * tests read; the rolling face is drawn over it with CSS content.
 */
export function RollingNumber({ value }: { value: number }) {
  const text = value.toLocaleString("ko-KR");
  const chars = [...text];
  return (
    <span className="roll">
      {/* The face comes first: an element after the number would split it from the next word in the accessible name. */}
      <span className="roll-face" aria-hidden="true">
        {chars.map((ch, i) => {
          // Keyed from the right, so a new leading digit does not reset the others.
          const key = chars.length - i;
          return /\d/.test(ch) ? (
            <span key={key} className="roll-col">
              {/* The column takes the current digit's own width (OS fonts have no tabular figures). */}
              <span className="roll-cur" data-ch={ch} />
              <span className="roll-strip" style={{ "--d": Number(ch) } as CSSProperties} />
            </span>
          ) : (
            <span key={key} className="roll-sep" data-ch={ch} />
          );
        })}
      </span>
      <span className="roll-text">{text}</span>
    </span>
  );
}

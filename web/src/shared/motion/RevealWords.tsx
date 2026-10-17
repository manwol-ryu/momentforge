import { Fragment, type CSSProperties } from "react";

/**
 * Text whose words rise into focus one after another (blur and offset
 * settling, 70 ms apart). Used once, for the home title; the words stay
 * ordinary text for screen readers and copying.
 */
export function RevealWords({ text, start = 0 }: { text: string; start?: number }) {
  return (
    <>
      {text.split(" ").map((word, i) => (
        <Fragment key={i}>
          {i > 0 && " "}
          <span className="reveal-w" style={{ "--i": start + i } as CSSProperties}>
            {word}
          </span>
        </Fragment>
      ))}
    </>
  );
}

import { useEffect, useRef, useState } from "react";

interface Props {
  /** The button's name, and the confirming button's (the same action keeps its name). */
  label: string;
  /** What happens, asked before it does: "수집을 멈추면 …". */
  question: string;
  /** The button that keeps things as they are. */
  keepLabel: string;
  onConfirm: () => void;
  disabled?: boolean;
  /** The first button's look ("button", "button button-quiet", "link-button"). */
  className?: string;
}

/**
 * An action that is hard to take back (stopping a capture, cancelling a
 * paid run, deleting a stored key), asked once more in place: the button
 * opens a short question with "keep" first, so a double click does not
 * confirm, and Escape closes it.
 */
export function ConfirmAction({ label, question, keepLabel, onConfirm, disabled = false, className = "button button-quiet" }: Props) {
  const [asking, setAsking] = useState(false);
  const keep = useRef<HTMLButtonElement>(null);
  const opener = useRef<HTMLButtonElement>(null);
  const reopened = useRef(false);

  useEffect(() => {
    if (asking) {
      keep.current?.focus();
      reopened.current = true;
    } else if (reopened.current) {
      reopened.current = false;
      opener.current?.focus();
    }
  }, [asking]);

  if (!asking) {
    return (
      <button ref={opener} type="button" className={className} onClick={() => setAsking(true)} disabled={disabled}>
        {label}
      </button>
    );
  }
  return (
    <span
      className="confirm"
      role="group"
      aria-label={`${label} 확인`}
      onKeyDown={(e) => {
        if (e.key !== "Escape") return;
        e.stopPropagation();
        setAsking(false);
      }}
    >
      <span className="confirm-question">{question}</span>
      <span className="confirm-actions">
        <button ref={keep} type="button" className="button button-quiet" onClick={() => setAsking(false)}>
          {keepLabel}
        </button>
        <button
          type="button"
          className="button"
          onClick={() => {
            setAsking(false);
            onConfirm();
          }}
          disabled={disabled}
        >
          {label}
        </button>
      </span>
    </span>
  );
}

import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type ReactNode,
} from "react";

export interface Tab<T extends string> {
  id: T;
  label: ReactNode;
  content: ReactNode;
}

interface Props<T extends string> {
  /** Accessible name of the tab list. */
  label: string;
  tabs: Tab<T>[];
  value: T;
  onChange: (id: T) => void;
  className?: string;
}

type Ink = { x: number; y: number; w: number; h: number };

/**
 * Panels behind tabs, with a pill behind the chosen tab that slides to the
 * next one (a spring, like the segmented pill; Tracwell's nav highlight). ARIA tabs: arrow keys,
 * Home and End move between tabs and select them. Every panel stays
 * mounted and only the chosen one is shown, so forms and scroll positions
 * survive switching.
 */
export function Tabs<T extends string>({ label, tabs, value, onChange, className }: Props<T>) {
  const base = useId();
  const list = useRef<HTMLDivElement>(null);
  const [ink, setInk] = useState<Ink | null>(null);
  const [ready, setReady] = useState(false);
  const keys = tabs.map((t) => t.id).join("|");

  useLayoutEffect(() => {
    const el = list.current;
    if (!el) return;
    const measure = () => {
      const on = el.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]');
      setInk(on ? { x: on.offsetLeft, y: on.offsetTop, w: on.offsetWidth, h: on.offsetHeight } : null);
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [value, keys]);

  useEffect(() => {
    const id = requestAnimationFrame(() => setReady(true));
    return () => cancelAnimationFrame(id);
  }, []);

  const tabId = (id: T) => `${base}-tab-${id}`;
  const panelId = (id: T) => `${base}-panel-${id}`;
  const move = (e: KeyboardEvent<HTMLButtonElement>) => {
    const at = tabs.findIndex((t) => t.id === value);
    const to =
      e.key === "ArrowRight" ? (at + 1) % tabs.length
      : e.key === "ArrowLeft" ? (at - 1 + tabs.length) % tabs.length
      : e.key === "Home" ? 0
      : e.key === "End" ? tabs.length - 1
      : -1;
    if (to < 0) return;
    e.preventDefault();
    const next = tabs[to]!;
    onChange(next.id);
    document.getElementById(tabId(next.id))?.focus();
  };

  return (
    <div className={className ? `tabs ${className}` : "tabs"}>
      <div ref={list} role="tablist" aria-label={label} className="tab-list" data-ready={ready || undefined}>
        {tabs.map((t) => {
          const on = t.id === value;
          return (
            <button
              key={t.id}
              type="button"
              role="tab"
              id={tabId(t.id)}
              aria-selected={on}
              aria-controls={panelId(t.id)}
              tabIndex={on ? 0 : -1}
              className="tab"
              onClick={() => onChange(t.id)}
              onKeyDown={move}
            >
              {t.label}
            </button>
          );
        })}
        {ink && <span className="tab-ink" aria-hidden="true" style={{ "--x": `${ink.x}px`, "--y": `${ink.y}px`, "--w": `${ink.w}px`, "--h": `${ink.h}px` } as CSSProperties} />}
      </div>
      {tabs.map((t) => (
        <div
          key={t.id}
          role="tabpanel"
          id={panelId(t.id)}
          aria-labelledby={tabId(t.id)}
          className="tab-panel"
          hidden={t.id !== value}
          tabIndex={0}
        >
          {t.content}
        </div>
      ))}
    </div>
  );
}

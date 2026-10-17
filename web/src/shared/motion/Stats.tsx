import type { ReactNode } from "react";

/**
 * A row of summary numbers, each a small card: its name above, the number
 * large below (Tracwell's overview cards). Counts inside roll when they change.
 */
export function Stats({ items, label = "요약" }: { items: { label: string; value: ReactNode }[]; label?: string }) {
  return (
    <dl className="stats" aria-label={label}>
      {items.map((it) => (
        <div key={it.label} className="stat">
          <dt>{it.label}</dt>
          <dd>{it.value}</dd>
        </div>
      ))}
    </dl>
  );
}

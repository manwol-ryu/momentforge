import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";

export interface SegmentOption<T extends string> {
  value: T;
  label: ReactNode;
}

interface Props<T extends string> {
  name: string;
  legend: string;
  hideLegend?: boolean;
  options: SegmentOption<T>[];
  value: T;
  onChange: (value: T) => void;
}

type Box = { x: number; y: number; w: number; h: number };

/**
 * A choice of one, drawn as segments with a pill that slides to the chosen
 * one. Underneath it is a plain radio group, so arrow keys, labels and
 * screen readers work as usual. The pill is measured from the chosen label
 * (also when options wrap to a second line) and only animates after it has
 * been placed once.
 */
export function Segmented<T extends string>({ name, legend, hideLegend = false, options, value, onChange }: Props<T>) {
  const track = useRef<HTMLDivElement>(null);
  const [pill, setPill] = useState<Box | null>(null);
  const [ready, setReady] = useState(false);
  const keys = options.map((o) => o.value).join("|");

  useLayoutEffect(() => {
    const el = track.current;
    if (!el) return;
    const measure = () => {
      const on = el.querySelector<HTMLElement>(".seg-opt[data-checked]");
      setPill(on ? { x: on.offsetLeft, y: on.offsetTop, w: on.offsetWidth, h: on.offsetHeight } : null);
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

  return (
    <fieldset className="seg">
      <legend className={hideLegend ? "visually-hidden" : "field-label"}>{legend}</legend>
      <div ref={track} className="seg-track" data-ready={ready || undefined}>
        {pill && (
          <span
            className="seg-pill"
            aria-hidden="true"
            style={{ "--x": `${pill.x}px`, "--y": `${pill.y}px`, "--w": `${pill.w}px`, "--h": `${pill.h}px` } as CSSProperties}
          />
        )}
        {options.map((o) => (
          <label key={o.value} className="seg-opt" data-checked={o.value === value || undefined}>
            <input type="radio" name={name} value={o.value} checked={o.value === value} onChange={() => onChange(o.value)} />
            {o.label}
          </label>
        ))}
      </div>
    </fieldset>
  );
}

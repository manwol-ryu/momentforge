import { useEffect, useState, type ReactNode } from "react";
import { Link } from "../navigation";

/**
 * Whether the element has scrolled up out of view. Returns a ref callback
 * to put on the element (it may appear only after data loads) and the
 * answer; an IntersectionObserver watches it, so nothing runs per scroll.
 */
export function useScrolledPast(): [(el: Element | null) => void, boolean] {
  const [el, setEl] = useState<Element | null>(null);
  const [past, setPast] = useState(false);
  useEffect(() => {
    if (!el) return;
    const io = new IntersectionObserver(([e]) => {
      if (e) setPast(!e.isIntersecting && e.boundingClientRect.top < 0);
    });
    io.observe(el);
    return () => io.disconnect();
  }, [el]);
  return [setEl, past];
}

/**
 * A slim bar that slides in at the top once the page title has scrolled
 * away, keeping the way home and what this page is in reach on long pages.
 * Hidden, it is inert, so it is skipped by the keyboard and screen readers.
 */
export function PageBar({ title, shown, children }: { title: string; shown: boolean; children?: ReactNode }) {
  return (
    <div className="page-bar" data-shown={shown || undefined} inert={!shown}>
      <div className="page-bar-in">
        <Link href="/" className="page-bar-home">
          MomentForge
        </Link>
        <span className="page-bar-title">{title}</span>
        {children}
      </div>
    </div>
  );
}

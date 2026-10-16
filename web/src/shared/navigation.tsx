import { useEffect, useState, type AnchorHTMLAttributes, type MouseEvent } from "react";

const CHANGE = "mf:navigate";

/** Current path; re-renders on Link clicks and browser back/forward. */
export function usePath(): string {
  const [path, setPath] = useState(window.location.pathname);
  useEffect(() => {
    const update = () => setPath(window.location.pathname);
    window.addEventListener("popstate", update);
    window.addEventListener(CHANGE, update);
    return () => {
      window.removeEventListener("popstate", update);
      window.removeEventListener(CHANGE, update);
    };
  }, []);
  return path;
}

export function navigate(to: string, { replace = false } = {}) {
  if (replace) window.history.replaceState(null, "", to);
  else window.history.pushState(null, "", to);
  window.dispatchEvent(new Event(CHANGE));
  window.scrollTo(0, 0);
}

/** An in-app link: a real <a href> that navigates without reloading. */
export function Link({ href, onClick, ...rest }: AnchorHTMLAttributes<HTMLAnchorElement> & { href: string }) {
  const handle = (e: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(e);
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    navigate(href);
  };
  return <a href={href} onClick={handle} {...rest} />;
}

import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "../shared/navigation";
import { BrandMark } from "./BrandMark";
import { Sidebar } from "./Sidebar";

const COLLAPSED_KEY = "mf.sidebar.collapsed";

function readCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSED_KEY) === "1";
  } catch {
    return false; // storage blocked: start open
  }
}

/**
 * The app frame (docs/DESIGN-SYSTEM.md 4): the saved-work list on the left
 * and the page beside it. On wide screens the list can fold into a slim
 * rail to give a workspace more room (remembered per browser); on phones it
 * is a drawer behind the top bar's menu button.
 */
export function Shell({ path, children }: { path: string; children: ReactNode }) {
  const queryClient = useQueryClient();
  const [drawer, setDrawer] = useState(false);
  const [collapsed, setCollapsed] = useState(readCollapsed);
  const menu = useRef<HTMLButtonElement>(null);
  const firstPath = useRef(true);

  // Another page: close the drawer and reload the list, whose chat counts grow while collecting.
  useEffect(() => {
    setDrawer(false);
    if (firstPath.current) {
      firstPath.current = false;
      return;
    }
    void queryClient.invalidateQueries({ queryKey: ["sessions"] });
  }, [path, queryClient]);

  // The open drawer takes the focus; Escape closes it and gives the focus back.
  useEffect(() => {
    if (!drawer) return;
    document.getElementById("sidebar")?.querySelector<HTMLElement>(".new-task")?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setDrawer(false);
      menu.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [drawer]);

  const fold = (value: boolean) => {
    setCollapsed(value);
    try {
      localStorage.setItem(COLLAPSED_KEY, value ? "1" : "0");
    } catch {
      // storage blocked: the choice lasts until reload
    }
  };

  return (
    <div className="shell" data-drawer={drawer || undefined} data-collapsed={collapsed || undefined}>
      <a href="#main" className="skip-link">
        본문으로 건너뛰기
      </a>
      <header className="topbar">
        <button
          ref={menu}
          type="button"
          className="menu-button"
          aria-expanded={drawer}
          aria-controls="sidebar"
          onClick={() => setDrawer((open) => !open)}
        >
          <span className="menu-icon" aria-hidden="true" />
          작업 목록
        </button>
        <Link href="/" className="brand">
          <BrandMark />
          MomentForge
        </Link>
      </header>
      <div className="rail">
        <button type="button" className="icon-button" aria-label="작업 목록 펼치기" onClick={() => fold(false)}>
          <span className="fold-icon" data-dir="open" aria-hidden="true" />
        </button>
        <Link href="/" className="icon-button" aria-label="새 작업" aria-current={path === "/" ? "page" : undefined}>
          <span className="plus-icon" aria-hidden="true" />
        </Link>
      </div>
      <Sidebar id="sidebar" path={path} onFold={() => fold(true)} />
      <div className="scrim" aria-hidden="true" onClick={() => setDrawer(false)} />
      <main id="main" className="shell-main" tabIndex={-1}>
        {children}
      </main>
    </div>
  );
}

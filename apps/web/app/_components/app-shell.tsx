import type { ReactNode } from "react";
import Link from "next/link";

type AppShellProps = {
  children: ReactNode;
};

export function AppShell({ children }: AppShellProps) {
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <header className="site-header">
        <Link className="brand" href="/" aria-label="Signa home">
          <span className="brand-mark" aria-hidden="true">
            s
          </span>
          <span>signa</span>
        </Link>
      </header>
      <main className="main-content" id="main-content">
        {children}
      </main>
      <footer className="site-footer">
        <span>Signa</span>
        <span>Local context matters.</span>
      </footer>
    </div>
  );
}

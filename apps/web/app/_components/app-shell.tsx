import type { ReactNode } from "react";
import Link from "next/link";

type AppShellProps = {
  children: ReactNode;
};

export function AppShell({ children }: AppShellProps) {
  return (
    <div className="flex min-h-svh flex-col bg-background text-foreground">
      <a
        className="absolute left-3 top-3 z-50 -translate-y-[160%] rounded-lg bg-foreground px-4 py-3 text-white transition-transform focus-visible:translate-y-0 focus-visible:outline-[var(--mint)]"
        href="#main-content"
      >
        Skip to content
      </a>
      <header className="mx-auto flex min-h-[4.5rem] w-[calc(100%_-_2rem)] max-w-304 items-center justify-between border-b border-border sm:min-h-[5.5rem] sm:w-[calc(100%_-_3rem)]">
        <Link
          className="inline-flex items-center gap-3 text-[1.2rem] font-bold tracking-[-0.045em] no-underline focus-visible:rounded-sm"
          href="/"
          aria-label="Signa home"
        >
          <span
            className="grid aspect-square w-8 place-items-center rounded-[0.65rem] bg-primary font-serif text-[1.3rem] font-normal text-primary-foreground"
            aria-hidden="true"
          >
            s
          </span>
          <span>signa</span>
        </Link>
      </header>
      <main
        className="flex min-w-0 flex-1 flex-col"
        id="main-content"
        tabIndex={-1}
      >
        {children}
      </main>
      <footer className="mx-auto flex min-h-16 w-[calc(100%_-_2rem)] max-w-304 items-center justify-between border-t border-border text-xs text-muted-foreground sm:min-h-[4.5rem] sm:w-[calc(100%_-_3rem)]">
        <span>Signa</span>
        <span>Local context matters.</span>
      </footer>
    </div>
  );
}

import { AppShell } from "./_components/app-shell";

export default function HomePage() {
  return (
    <AppShell>
      <div className="mx-auto grid w-[calc(100%_-_2rem)] max-w-304 flex-1 grid-cols-1 items-center gap-12 py-18 sm:w-[calc(100%_-_3rem)] md:grid-cols-[minmax(0,1fr)_minmax(18rem,0.9fr)] md:gap-[clamp(2rem,8vw,8rem)] md:py-[clamp(4rem,11vh,8rem)]">
        <section
          className="relative z-10 max-w-144"
          aria-labelledby="welcome-title"
        >
          <p className="mb-6 text-xs font-bold tracking-[0.14em] text-primary uppercase">
            Community intelligence
          </p>
          <h1
            className="m-0 max-w-[9ch] font-serif text-[clamp(3.1rem,13vw,4.5rem)] leading-[0.99] font-normal tracking-[-0.065em] md:max-w-[10ch] md:text-[clamp(3.25rem,6vw,5.7rem)]"
            id="welcome-title"
          >
            Local context matters.
          </h1>
          <p className="mt-7 max-w-112 text-[clamp(1rem,1.5vw,1.15rem)] leading-[1.75] text-muted-foreground">
            Signa is being built to help communities make sense of timely local
            information.
          </p>
          <div className="mt-8 flex items-center gap-3 text-sm font-semibold text-foreground md:mt-10">
            <span
              className="grid aspect-square w-8 place-items-center rounded-full border border-border text-base text-primary"
              aria-hidden="true"
            >
              ↗
            </span>
            <span>A clear foundation for what comes next.</span>
          </div>
        </section>
        <div
          className="relative mx-auto grid aspect-square w-[min(72vw,19rem)] place-items-center rounded-full border border-primary/10 bg-[radial-gradient(circle,rgb(255_255_255_/_75%),transparent_68%)] md:mx-0 md:ml-auto md:w-[min(100%,27rem)]"
          aria-hidden="true"
        >
          <div className="absolute aspect-square w-[83%] animate-arrive rounded-full border border-primary/15 motion-reduce:animate-none" />
          <div className="absolute aspect-square w-[62%] animate-arrive-delayed-100 rounded-full border border-primary/15 motion-reduce:animate-none" />
          <div className="absolute aspect-square w-[40%] animate-arrive-delayed-200 rounded-full border border-primary/25 motion-reduce:animate-none" />
          <span className="grid aspect-square w-16 place-items-center rounded-[1.25rem] bg-primary font-serif text-[2.4rem] text-primary-foreground shadow-[0_1.5rem_3rem_rgb(23_106_87_/_22%)] md:w-20 md:rounded-[1.6rem] md:text-5xl">
            s
          </span>
          <span className="absolute top-[18%] right-[27%] aspect-square w-3 rounded-full border-[3px] border-background bg-[#d08b56] shadow-[0_0_0_1px_rgb(23_106_87_/_14%)]" />
          <span className="absolute bottom-[23%] left-[18%] aspect-square w-[0.65rem] rounded-full border-[3px] border-background bg-primary shadow-[0_0_0_1px_rgb(23_106_87_/_14%)]" />
        </div>
      </div>
    </AppShell>
  );
}

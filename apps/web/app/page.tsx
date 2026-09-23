import { ReportForm } from "./_components/report-form";
import { AppShell } from "./_components/app-shell";

export default function HomePage() {
  return (
    <AppShell>
      <div className="mx-auto flex w-[calc(100%_-_2rem)] max-w-304 flex-1 flex-col py-8 sm:w-[calc(100%_-_3rem)] sm:py-12">
        <div className="mx-auto w-full max-w-md">
          <p className="mb-3 text-xs font-bold tracking-[0.14em] text-primary uppercase">
            Community report
          </p>
          <h1 className="text-3xl leading-tight font-semibold tracking-[-0.04em] sm:text-4xl">
            Report an event
          </h1>
          <p className="mt-3 max-w-2xl text-base leading-7 text-muted-foreground">
            Tell us what you saw or heard.
          </p>

          <section
            aria-labelledby="report-form-title"
            className="mt-8 rounded-xl border border-border bg-card p-5 shadow-sm sm:mt-10 sm:p-8"
          >
            <h2 className="sr-only" id="report-form-title">
              Submit a report
            </h2>
            <ReportForm />
          </section>

          <p className="mt-5 text-sm leading-6 text-muted-foreground">
            Reports are sent to Signa for processing.
          </p>
        </div>
      </div>
    </AppShell>
  );
}

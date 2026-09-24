import { AppShell } from "../../_components/app-shell";
import { PushSettings } from "./_components/push-settings";

export default function NotificationSettingsPage() {
  return (
    <AppShell>
      <div className="mx-auto w-full max-w-2xl flex-1 px-4 py-8 sm:px-6 sm:py-12">
        <p className="mb-3 text-xs font-bold tracking-[0.14em] text-primary uppercase">
          Settings
        </p>
        <h1 className="text-3xl leading-tight font-semibold tracking-[-0.04em] sm:text-4xl">
          Notifications
        </h1>
        <p className="mt-3 max-w-xl text-base leading-7 text-muted-foreground">
          Choose whether this browser can receive Signa notifications.
        </p>
        <section
          aria-labelledby="push-settings-title"
          className="mt-8 rounded-xl border border-border bg-card p-5 shadow-sm sm:mt-10 sm:p-8"
        >
          <h2 id="push-settings-title" className="sr-only">
            Browser notifications
          </h2>
          <PushSettings />
        </section>
      </div>
    </AppShell>
  );
}

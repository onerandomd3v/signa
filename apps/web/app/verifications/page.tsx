import type { Metadata } from "next";
import { AppShell } from "../_components/app-shell";
import { VerificationInbox } from "./_components/verification-inbox";

export const metadata: Metadata = {
  title: "Verifier inbox | Signa",
  description: "Review active requests using what you already know.",
};

export default function VerificationInboxPage() {
  return (
    <AppShell>
      <section className="mx-auto w-full max-w-3xl px-4 py-8 sm:px-6 sm:py-12">
        <header className="mb-6 sm:mb-8">
          <p className="text-sm font-semibold text-primary">Trusted verifier</p>
          <h1 className="mt-2 text-3xl leading-tight font-semibold tracking-[-0.04em] sm:text-4xl">
            Verification inbox
          </h1>
          <p className="mt-2 max-w-xl text-sm leading-6 text-muted-foreground">
            Share only what you already know or observed.
          </p>
        </header>
        <VerificationInbox />
      </section>
    </AppShell>
  );
}

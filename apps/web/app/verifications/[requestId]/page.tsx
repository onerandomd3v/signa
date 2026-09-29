import type { Metadata } from "next";
import Link from "next/link";
import { AppShell } from "../../_components/app-shell";
import { VerificationDetailExperience } from "../_components/verification-detail-experience";

export const metadata: Metadata = {
  title: "Verification request | Signa",
  description: "Respond only from what you already know or observed.",
};

export default async function VerificationRequestPage({
  params,
}: {
  params: Promise<{ requestId: string }>;
}) {
  const { requestId } = await params;

  return (
    <AppShell>
      <section className="mx-auto w-full max-w-3xl px-4 py-6 sm:px-6 sm:py-10">
        <nav aria-label="Breadcrumb" className="mb-5">
          <Link
            className="inline-flex min-h-11 items-center rounded-md text-sm font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"
            href="/verifications"
          >
            Verification inbox
          </Link>
        </nav>
        <VerificationDetailExperience requestId={requestId} />
      </section>
    </AppShell>
  );
}

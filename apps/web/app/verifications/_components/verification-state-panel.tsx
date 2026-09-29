import type { ReactNode } from "react";

export function VerificationStatePanel({
  title,
  children,
  role = "status",
}: {
  title: string;
  children: ReactNode;
  role?: "status" | "alert";
}) {
  return (
    <section
      aria-live={role === "alert" ? "assertive" : "polite"}
      className="rounded-xl border border-border bg-card p-5 shadow-sm sm:p-7"
      role={role}
    >
      <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
      <div className="mt-2 text-sm leading-6 text-muted-foreground">
        {children}
      </div>
    </section>
  );
}

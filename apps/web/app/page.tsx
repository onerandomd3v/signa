import { AppShell } from "./_components/app-shell";

export default function HomePage() {
  return (
    <AppShell>
      <section className="welcome" aria-labelledby="welcome-title">
        <p className="eyebrow">Community intelligence</p>
        <h1 id="welcome-title">Local context matters.</h1>
        <p className="welcome-copy">
          Signa is being built to help communities make sense of timely local
          information.
        </p>
        <div className="welcome-note">
          <span className="welcome-note-mark" aria-hidden="true">
            ↗
          </span>
          <span>A clear foundation for what comes next.</span>
        </div>
      </section>
      <div className="visual" aria-hidden="true">
        <div className="visual-ring visual-ring-outer" />
        <div className="visual-ring visual-ring-middle" />
        <div className="visual-ring visual-ring-inner" />
        <span className="visual-center">s</span>
        <span className="visual-point visual-point-one" />
        <span className="visual-point visual-point-two" />
      </div>
    </AppShell>
  );
}

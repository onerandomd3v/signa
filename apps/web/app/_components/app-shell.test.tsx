import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AppShell } from "./app-shell";

describe("AppShell", () => {
  it("provides a branded, accessible shell around page content", () => {
    render(
      <AppShell>
        <h1>Local context matters.</h1>
      </AppShell>,
    );

    const homeLink = screen.getByRole("link", { name: "Signa home" });
    const skipLink = screen.getByRole("link", { name: "Skip to content" });
    const main = screen.getByRole("main");

    expect(screen.getByRole("banner")).toBeTruthy();
    expect(homeLink.getAttribute("href")).toBe("/");
    expect(
      screen.getByRole("navigation", { name: "Main navigation" }).textContent,
    ).toContain("Incidents");
    expect(skipLink.getAttribute("href")).toBe("#main-content");
    expect(main.getAttribute("tabindex")).toBe("-1");
    expect(
      main.contains(
        screen.getByRole("heading", { name: "Local context matters." }),
      ),
    ).toBe(true);
  });
});

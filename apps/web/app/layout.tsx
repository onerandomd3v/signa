import type { Metadata } from "next";
import type { ReactNode } from "react";
import "./globals.css";
import "maplibre-gl/dist/maplibre-gl.css";

export const metadata: Metadata = {
  title: "Submit a community report | Signa",
  description:
    "Share a brief text report about what you observed in your community.",
};

export default function RootLayout({
  children,
}: Readonly<{ children: ReactNode }>) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}

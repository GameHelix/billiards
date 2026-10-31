import type { Metadata, Viewport } from "next";
import "./globals.css";

/** An 8-ball, inline, so the tab has an icon without any asset file. */
const ICON =
  "data:image/svg+xml," +
  encodeURIComponent(
    "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'>" +
      "<defs><radialGradient id='g' cx='36%' cy='30%' r='75%'><stop offset='0' stop-color='#5b4b8a'/>" +
      "<stop offset='1' stop-color='#070610'/></radialGradient></defs>" +
      "<circle cx='32' cy='32' r='29' fill='url(#g)' stroke='#22d3ee' stroke-width='3'/>" +
      "<circle cx='32' cy='30' r='13' fill='#f8fafc'/>" +
      "<text x='32' y='36.5' font-family='Arial,sans-serif' font-weight='700' font-size='18' text-anchor='middle' fill='#070610'>8</text>" +
      "</svg>"
  );

export const metadata: Metadata = {
  title: "Billiards — neon 8-ball pool",
  icons: { icon: ICON },
  description:
    "Neon 8-ball pool built with Next.js and TypeScript. Break, take solids or stripes and sink the 8 against a CPU (Easy, Medium or Hard) or a friend, on a table with real ball physics: spin, cut angles, rattling pocket jaws and a full set of fouls.",
};

export const viewport: Viewport = {
  themeColor: "#070610",
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}

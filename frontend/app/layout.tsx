import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "LARA Radio Controller",
  description: "Virtual LARA panel for ELKO EP LARA Radio/Intercom",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="cs" className="h-full">
      <body className="min-h-full flex flex-col">{children}</body>
    </html>
  );
}

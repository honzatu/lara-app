"use client";
import LaraPanel from "./components/LaraPanel";

export default function Home() {
  return (
    <main className="min-h-screen flex flex-col items-center justify-center gap-4 p-4"
      style={{ background: "linear-gradient(135deg, #c8c8c8 0%, #e8e8e8 100%)" }}>
      <LaraPanel deviceId={1} deviceName="Kuchyne" />
    </main>
  );
}

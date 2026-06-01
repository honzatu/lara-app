"use client";
import { useEffect, useState } from "react";
import { PlayerState } from "./LaraPanel";

interface Props {
  state: PlayerState;
  deviceName: string;
}

export default function LaraDisplay({ state, deviceName }: Props) {
  // mounted flag prevents SSR/hydration mismatch for time
  const [mounted, setMounted] = useState(false);
  const [time, setTime] = useState("--:--");
  const [date, setDate] = useState("--.--.----");

  useEffect(() => {
    setMounted(true);
    const update = () => {
      const now = new Date();
      const h = String(now.getHours()).padStart(2, "0");
      const m = String(now.getMinutes()).padStart(2, "0");
      const d = String(now.getDate()).padStart(2, "0");
      const mo = String(now.getMonth() + 1).padStart(2, "0");
      setTime(`${h}:${m}`);
      setDate(`${d}.${mo}.${now.getFullYear()}`);
    };
    update();
    const id = setInterval(update, 1000);
    return () => clearInterval(id);
  }, []);

  const stationName = state.stationName || deviceName || "—";
  const trackTitle = state.trackTitle
    ? state.trackTitle
    : state.playing ? "PŘEHRÁVÁM..." : "ZASTAVENO";

  return (
    <div
      style={{
        background: "#000",
        borderRadius: 4,
        padding: "6px 8px",
        height: 200,
        display: "flex",
        flexDirection: "column",
        gap: 2,
        overflow: "hidden",
        boxShadow: "inset 0 0 8px rgba(0,0,0,0.8), 0 0 2px rgba(0,0,0,0.5)",
        border: "1px solid #222",
      }}
    >
      {/* Row 1: Date + Time */}
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline" }}>
        <span style={{ color: "#d4b800", fontSize: 11, fontFamily: "monospace", fontWeight: "bold" }}>
          {date}
        </span>
        <span style={{ color: "#d4b800", fontSize: 11, fontFamily: "monospace", fontWeight: "bold" }}>
          {time}
        </span>
      </div>

      {/* Row 2: STANICE label */}
      <div style={{ color: "#00a8e8", fontSize: 9, fontFamily: "monospace", letterSpacing: 1 }}>
        STANICE:
      </div>

      {/* Row 3: Station name (scrolling if long) */}
      <div style={{ overflow: "hidden", height: 22 }}>
        <div
          style={{
            color: "#ffffff",
            fontSize: 15,
            fontWeight: "bold",
            fontFamily: "monospace",
            whiteSpace: "nowrap",
            lineHeight: "22px",
          }}
          className={stationName.length > 14 ? "marquee" : ""}
        >
          {stationName.toUpperCase()}
        </div>
      </div>

      {/* Row 4: PRÁVĚ HRAJE label */}
      <div style={{ color: "#00a8e8", fontSize: 9, fontFamily: "monospace", letterSpacing: 1 }}>
        PRÁVĚ HRAJE:
      </div>

      {/* Row 5: Track title */}
      <div style={{ overflow: "hidden", height: 16 }}>
        <div
          style={{
            color: "#ffffff",
            fontSize: 11,
            fontFamily: "monospace",
            whiteSpace: "nowrap",
            lineHeight: "16px",
          }}
          className={trackTitle.length > 18 ? "marquee" : ""}
        >
          {trackTitle}
        </div>
      </div>

      {/* Row 6: EQ bars */}
      <div style={{ display: "flex", alignItems: "flex-end", gap: 2, height: 40, marginTop: 2 }}>
        {Array.from({ length: 16 }).map((_, i) => (
          <div
            key={i}
            className={`eq-bar${!state.playing ? " stopped" : ""}`}
            style={{
              flex: 1,
              height: "100%",
              background: i < 10 ? "#c8d400" : "#88e000",
              borderRadius: 1,
            }}
          />
        ))}
      </div>

      {/* Row 7: Status bar */}
      <div style={{ display: "flex", alignItems: "center", gap: 4, marginTop: 2 }}>
        {/* MP3 badge */}
        <div style={{
          background: "#0050c8",
          color: "#fff",
          fontSize: 7,
          fontFamily: "monospace",
          fontWeight: "bold",
          padding: "1px 3px",
          borderRadius: 1,
          lineHeight: 1.4,
        }}>
          MP3<br/>128
        </div>
        {/* Status text */}
        <span style={{ color: "#ccc", fontSize: 9, fontFamily: "monospace", flex: 1 }}>
          {state.playing ? "PŘEHRÁVÁM.." : "STOP"}
        </span>
        {/* Volume indicator */}
        <span style={{ color: "#aaa", fontSize: 9, fontFamily: "monospace" }}>
          {state.volume}
        </span>
        {/* Signal bars */}
        <SignalBars volume={state.volume} />
      </div>
    </div>
  );
}

function SignalBars({ volume }: { volume: number }) {
  const bars = 5;
  const filled = Math.round((volume / 100) * bars);
  return (
    <div style={{ display: "flex", alignItems: "flex-end", gap: 1 }}>
      {Array.from({ length: bars }).map((_, i) => (
        <div
          key={i}
          style={{
            width: 3,
            height: 4 + i * 2,
            background: i < filled ? "#4ade80" : "#333",
            borderRadius: 1,
          }}
        />
      ))}
    </div>
  );
}

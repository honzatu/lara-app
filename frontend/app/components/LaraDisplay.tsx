"use client";
// ─── LAYOUT RULES — DO NOT CHANGE WITHOUT GOOD REASON ────────────────────────
// height:200px fixed — fits inside 320×320 panel with header + bottom row
// EQ bars: flex:1 + minHeight:28 — fills remaining space between status and top
// Rows with nowrap text MUST have overflow:hidden + fixed height (prevents flex blowout)
// ─────────────────────────────────────────────────────────────────────────────
import { useEffect, useRef, useState } from "react";
import { PlayerState } from "./LaraPanel";

interface Props {
  state: PlayerState;
  deviceName: string;
}

export default function LaraDisplay({ state, deviceName }: Props) {
  const [time, setTime] = useState("--:--");
  const [date, setDate] = useState("--.--.----");

  useEffect(() => {
    const update = () => {
      const now = new Date();
      setTime(`${String(now.getHours()).padStart(2,"0")}:${String(now.getMinutes()).padStart(2,"0")}`);
      setDate(`${String(now.getDate()).padStart(2,"0")}.${String(now.getMonth()+1).padStart(2,"0")}.${now.getFullYear()}`);
    };
    update();
    const id = setInterval(update, 1000);
    return () => clearInterval(id);
  }, []);

  // ── Web Audio API — real EQ visualization ──────────────────────────────
  const audioRef    = useRef<HTMLAudioElement | null>(null);
  const ctxRef      = useRef<AudioContext | null>(null);
  const analyserRef = useRef<AnalyserNode | null>(null);
  const rafRef      = useRef<number>(0);
  const [eqBars, setEqBars] = useState<number[]>(new Array(16).fill(0));

  useEffect(() => {
    const API = process.env.NEXT_PUBLIC_API_URL || "http://192.168.1.3:8400";
    if (!state.playing || !state.streamUrl) {
      // Stop analyzer
      cancelAnimationFrame(rafRef.current);
      audioRef.current?.pause();
      ctxRef.current?.close().catch(() => {});
      ctxRef.current = null;
      setEqBars(new Array(16).fill(0));
      return;
    }

    const proxyUrl = `${API}/stream/radio?url=${encodeURIComponent(state.streamUrl)}`;

    const setup = async () => {
      try {
        const ctx = new AudioContext();
        ctxRef.current = ctx;
        if (ctx.state === "suspended") await ctx.resume();

        const analyser = ctx.createAnalyser();
        analyser.fftSize = 64;
        analyser.smoothingTimeConstant = 0.75;
        analyserRef.current = analyser;

        const gain = ctx.createGain();
        gain.gain.value = 0; // Silent — only analyze

        const audio = new Audio();
        audio.crossOrigin = "anonymous";
        audio.src = proxyUrl;
        audioRef.current = audio;


        const src = ctx.createMediaElementSource(audio);
        src.connect(analyser);
        src.connect(gain);
        gain.connect(ctx.destination);

        await audio.play();

        const data = new Uint8Array(analyser.frequencyBinCount);
        const tick = () => {
          analyser.getByteFrequencyData(data);
          const max = Math.max(...data);
          if (max > 0) {
            setEqBars(Array.from({ length: 16 }, (_, i) =>
              (data[i * 2] + data[i * 2 + 1]) / 2 / 255
            ));
          }
          rafRef.current = requestAnimationFrame(tick);
        };
        tick();
      } catch (e) {
        console.warn("[LARA EQ] Failed:", e);
      }
    };

    setup();

    return () => {
      cancelAnimationFrame(rafRef.current);
      audioRef.current?.pause();
      ctxRef.current?.close().catch(() => {});
      ctxRef.current = null;
    };
  }, [state.playing, state.streamUrl]);
  // ───────────────────────────────────────────────────────────────────────

  const stationName = state.stationName || deviceName || "—";
  const trackTitle  = state.trackTitle || (state.playing ? "PŘEHRÁVÁM..." : "ZASTAVENO");
  const progressStr = state.duration > 0
    ? `${fmt(state.elapsed)}/${fmt(state.duration)}`
    : state.playing ? "LIVE" : "";

  return (
    <div style={{
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
    }}>

      {/* Row 1: Date + Time */}
      <div style={{ display:"flex", justifyContent:"space-between", alignItems:"baseline" }}>
        <span style={{ color:"#d4b800", fontSize:11, fontFamily:"monospace", fontWeight:"bold" }}>{date}</span>
        <span style={{ color:"#d4b800", fontSize:11, fontFamily:"monospace", fontWeight:"bold" }}>{time}</span>
      </div>

      {/* Row 2: STANICE label */}
      <div style={{ color:"#00a8e8", fontSize:9, fontFamily:"monospace", letterSpacing:1 }}>STANICE:</div>

      {/* Row 3: Station name */}
      <div style={{ overflow:"hidden", height:22 }}>
        <div
          style={{ color:"#fff", fontSize:15, fontWeight:"bold", fontFamily:"monospace", whiteSpace:"nowrap", lineHeight:"22px" }}
          className={stationName.length > 14 ? "marquee" : ""}
        >
          {stationName.toUpperCase()}
        </div>
      </div>

      {/* Row 4: PRÁVĚ HRAJE label */}
      <div style={{ color:"#00a8e8", fontSize:9, fontFamily:"monospace", letterSpacing:1 }}>PRÁVĚ HRAJE:</div>

      {/* Row 5: Track title */}
      <div style={{ overflow:"hidden", height:16 }}>
        <div
          style={{ color:"#fff", fontSize:11, fontFamily:"monospace", whiteSpace:"nowrap", lineHeight:"16px" }}
          className={trackTitle.length > 18 ? "marquee" : ""}
        >
          {trackTitle}
        </div>
      </div>

      {/* Row 6: EQ bars — real FFT data via Web Audio API, fallback CSS anim */}
      <div style={{ display:"flex", alignItems:"flex-end", gap:2, flex:1, minHeight:28 }}>
        {Array.from({ length: 16 }).map((_, i) => {
          const hasReal = eqBars.some(v => v > 0);
          return (
            <div
              key={i}
              className={hasReal ? "" : `eq-bar${!state.playing ? " stopped" : ""}`}
              style={{
                flex: 1,
                height: hasReal ? `${Math.max(8, eqBars[i] * 100)}%` : "100%",
                background: i < 10 ? "#c8d400" : "#88e000",
                borderRadius: 1,
                transition: hasReal ? "height 0.08s ease-out" : undefined,
              }}
            />
          );
        })}
      </div>

      {/* Row 7: Status bar — pod EQ */}
      <div style={{ display:"flex", alignItems:"center", gap:4 }}>
        <div style={{
          background:"#0050c8", color:"#fff", fontSize:7, fontFamily:"monospace",
          fontWeight:"bold", padding:"1px 3px", borderRadius:1, lineHeight:1.4,
        }}>MP3<br/>128</div>
        <span style={{ color:"#ccc", fontSize:9, fontFamily:"monospace", flex:1 }}>
          {state.playing ? "PŘEHRÁVÁM.." : "STOP"}
        </span>
        {progressStr && (
          <span style={{ color:"#d4b800", fontSize:9, fontFamily:"monospace" }}>{progressStr}</span>
        )}
        <span style={{ color:"#aaa", fontSize:9, fontFamily:"monospace" }}>{state.volume}</span>
        <SignalBars volume={state.volume} />
      </div>

    </div>
  );
}

function fmt(s: number) {
  return `${String(Math.floor(s/60)).padStart(2,"0")}:${String(Math.floor(s%60)).padStart(2,"0")}`;
}

function SignalBars({ volume }: { volume: number }) {
  const filled = Math.round((volume / 100) * 5);
  return (
    <div style={{ display:"flex", alignItems:"flex-end", gap:1 }}>
      {Array.from({ length: 5 }).map((_, i) => (
        <div key={i} style={{
          width:3, height: 4 + i * 2,
          background: i < filled ? "#4ade80" : "#333",
          borderRadius:1,
        }} />
      ))}
    </div>
  );
}

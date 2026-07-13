"use client";
// ─── LAYOUT RULES — DO NOT CHANGE WITHOUT GOOD REASON ────────────────────────
// Panel:   320×320px fixed, overflow:hidden, padding:16px
// Display: height:200px fixed, wrapper minWidth:0 + overflow:hidden (prevents
//          white-space:nowrap marquee text from pushing flex width)
// Buttons: gap-4 left col (4 btns), gap-6 right col (2 btns), items-center row
// ─────────────────────────────────────────────────────────────────────────────
import { useState, useEffect, useCallback } from "react";
import LaraDisplay from "./LaraDisplay";
import RadioBrowser from "./RadioBrowser";

const API = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8400";

interface Props {
  deviceId: number;
  deviceName: string;
}

export interface PlayerState {
  playing: boolean;
  volume: number;
  stationIndex: number;
  stationName: string;
  trackTitle: string;
  artist: string;
  elapsed: number;
  duration: number;
  streamUrl: string;
  muted: boolean;
}

export default function LaraPanel({ deviceId, deviceName }: Props) {
  const [state, setState] = useState<PlayerState>({
    playing: false,
    volume: 50,
    stationIndex: 0,
    stationName: "—",
    trackTitle: "",
    artist: "",
    elapsed: 0,
    duration: 0,
    streamUrl: "",
    muted: false,
  });

  const [loading, setLoading] = useState(false);
  const [browserOpen, setBrowserOpen] = useState(false);

  // Poll status every 3s — updates playing state and track title from LMS
  useEffect(() => {
    const poll = async () => {
      try {
        const r = await fetch(`${API}/api/v1/devices/${deviceId}/status`);
        if (!r.ok) return;
        const data = await r.json();
        setState(prev => ({
          ...prev,
          playing: data.playing,
          volume: data.volume ?? prev.volume,
          stationName: data.station_name || prev.stationName,
          trackTitle: data.track_title || prev.trackTitle,
          artist: data.artist || prev.artist,
          elapsed: data.elapsed ?? prev.elapsed,
          duration: data.duration ?? prev.duration,
          streamUrl: data.stream_url || prev.streamUrl,
        }));
      } catch {}
    };
    poll();
    const id = setInterval(poll, 3000);
    return () => clearInterval(id);
  }, [deviceId]);

  const api = useCallback((path: string, body?: object): void => {
    setLoading(true);
    fetch(`${API}/api/v1/devices/${deviceId}/${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
    })
      .catch(err => console.error("[LARA]", path, err))
      .finally(() => setLoading(false));
  }, [deviceId]);

  // Play/Pause — toggles LMS pause/resume (preserves stream state)
  const onPlayPause = () => {
    if (state.playing) {
      api("pause");
      setState(p => ({ ...p, playing: false }));
    } else {
      api("play");
      setState(p => ({ ...p, playing: true }));
    }
  };

  // Power/Stop — full stop, clears LMS stream
  const onStop = () => {
    api("stop");
    setState(p => ({ ...p, playing: false, trackTitle: "", artist: "" }));
  };

  const onMute = () => {
    api("mute");
    setState(p => ({ ...p, muted: !p.muted }));
  };

  const onVolUp = () => {
    const newVol = Math.min(100, state.volume + 5);
    api("volume", { volume: newVol });
    setState(p => ({ ...p, volume: newVol }));
  };

  const onVolDown = () => {
    const newVol = Math.max(0, state.volume - 5);
    api("volume", { volume: newVol });
    setState(p => ({ ...p, volume: newVol }));
  };

  const onNext = () => api("skip");
  const onPrev = () => api("prev");

  return (
    <div
      className="relative select-none"
      style={{
        width: 320,
        height: 320,
        borderRadius: 12,
        background: "linear-gradient(145deg, #f0f0f0 0%, #d8d8d8 100%)",
        boxShadow: "0 8px 32px rgba(0,0,0,0.35), inset 0 1px 0 rgba(255,255,255,0.8)",
        border: "1px solid #bbb",
        padding: 16,
        overflow: "hidden",
      }}
    >
      {/* Header */}
      <div className="flex justify-between items-center mb-2" style={{ paddingLeft: 2, paddingRight: 2 }}>
        <span style={{ fontSize: 11, color: "#555", fontWeight: 600, letterSpacing: 1 }}>LARA</span>
        <span style={{ fontSize: 9, color: "#777", fontStyle: "italic", letterSpacing: 0.5 }}>ELKO</span>
      </div>

      {/* Main body: buttons + display + buttons */}
      <div className="flex items-center gap-2">

        {/* Left buttons */}
        <div className="flex flex-col gap-4 items-center" style={{ width: 32 }}>
          <button className="lara-btn" onClick={onMute} title="Mute"><MuteIcon muted={state.muted} /></button>
          <button className="lara-btn" onClick={onStop} title="Stop"><PowerIcon /></button>
          <button className="lara-btn" onClick={onPlayPause} title={state.playing ? "Pause" : "Play"} disabled={loading}><PlayPauseIcon playing={state.playing} /></button>
          <button className="lara-btn" onClick={() => setBrowserOpen(true)} title="Browse stations & music"><SettingsIcon /></button>
        </div>

        {/* OLED Display — minWidth:0 prevents nowrap text from pushing flex width */}
        <div className="flex-1" style={{ minWidth: 0, overflow: "hidden" }}>
          <LaraDisplay state={state} deviceName={deviceName} />
        </div>

        {/* Right buttons */}
        <div className="flex flex-col gap-6 items-center" style={{ width: 32 }}>
          <button className="lara-btn" onClick={onVolUp} title="Volume +"><VolUpIcon /></button>
          <button className="lara-btn" onClick={onVolDown} title="Volume -"><VolDownIcon /></button>
        </div>

      </div>

      {/* Bottom row */}
      <div className="flex items-center justify-between mt-3" style={{ paddingLeft: 36, paddingRight: 36 }}>
        <button className="lara-btn" onClick={onPrev} title="Previous"><PrevIcon /></button>
        <div className={state.playing ? "led-on" : "led-off"} style={{ width: 3, height: 20, borderRadius: 2, background: state.playing ? "#4ade80" : "#555" }} />
        <button className="lara-btn" onClick={onNext} title="Next"><NextIcon /></button>
        <div style={{ width: 8, height: 8, borderRadius: "50%", background: state.playing ? "#4ade80" : "#888", boxShadow: state.playing ? "0 0 6px #4ade80" : "none" }} />
      </div>

      {browserOpen && (
        <RadioBrowser
          deviceId={deviceId}
          onClose={() => setBrowserOpen(false)}
          onPlayed={name => setState(p => ({ ...p, playing: true, stationName: name, trackTitle: "", artist: "" }))}
        />
      )}
    </div>
  );
}

// --- Icons (SVG, white/gray, matching physical LARA style) ---

function MuteIcon({ muted }: { muted: boolean }) {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
      <path d="M11 5L6 9H2v6h4l5 4V5z" fill={muted ? "#888" : "#444"} />
      {muted ? (
        <>
          <line x1="17" y1="9" x2="23" y2="15" stroke="#444" strokeWidth="2" strokeLinecap="round"/>
          <line x1="23" y1="9" x2="17" y2="15" stroke="#444" strokeWidth="2" strokeLinecap="round"/>
        </>
      ) : (
        <path d="M15.54 8.46a5 5 0 010 7.07" stroke="#444" strokeWidth="2" strokeLinecap="round"/>
      )}
    </svg>
  );
}

function PowerIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
      <path d="M12 2v10" stroke="#444" strokeWidth="2.5" strokeLinecap="round"/>
      <path d="M18.36 5.64a9 9 0 11-12.72 0" stroke="#444" strokeWidth="2.5" strokeLinecap="round"/>
    </svg>
  );
}

function PlayPauseIcon({ playing }: { playing: boolean }) {
  return playing ? (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none">
      <rect x="6" y="5" width="4" height="14" rx="1" fill="#333"/>
      <rect x="14" y="5" width="4" height="14" rx="1" fill="#333"/>
    </svg>
  ) : (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none">
      <polygon points="6,4 20,12 6,20" fill="#333"/>
    </svg>
  );
}

function SettingsIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
      <circle cx="12" cy="12" r="3" stroke="#444" strokeWidth="2"/>
      <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z" stroke="#444" strokeWidth="1.5"/>
    </svg>
  );
}

function VolUpIcon() {
  return (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none">
      <path d="M11 5L6 9H2v6h4l5 4V5z" fill="#444"/>
      <path d="M15.54 8.46a5 5 0 010 7.07M19.07 4.93a10 10 0 010 14.14" stroke="#444" strokeWidth="2" strokeLinecap="round"/>
    </svg>
  );
}

function VolDownIcon() {
  return (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none">
      <path d="M11 5L6 9H2v6h4l5 4V5z" fill="#444"/>
      <path d="M15.54 8.46a5 5 0 010 7.07" stroke="#444" strokeWidth="2" strokeLinecap="round"/>
    </svg>
  );
}

function PrevIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
      <polygon points="19,5 9,12 19,19" fill="#444"/>
      <rect x="5" y="5" width="2.5" height="14" rx="1" fill="#444"/>
    </svg>
  );
}

function NextIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
      <polygon points="5,5 15,12 5,19" fill="#444"/>
      <rect x="16.5" y="5" width="2.5" height="14" rx="1" fill="#444"/>
    </svg>
  );
}

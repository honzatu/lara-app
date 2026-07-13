"use client";
// Station browser modal — radio search (Radio Browser API), favorites
// with physical-slot sync, and YouTube Music search via the music bridge.
import { useState, useEffect, useCallback } from "react";

const API = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8400";

interface RadioStation {
  name: string;
  url_resolved: string;
  url: string;
  country: string;
  bitrate: number;
}

interface Favorite {
  id: number;
  name: string;
  url: string;
}

interface Song {
  id: string;
  title: string;
  artist: string;
  duration: string;
}

interface Props {
  deviceId: number;
  onClose: () => void;
  onPlayed: (name: string) => void;
}

export default function RadioBrowser({ deviceId, onClose, onPlayed }: Props) {
  const [tab, setTab] = useState<"radio" | "youtube">("radio");
  const [query, setQuery] = useState("");
  const [stations, setStations] = useState<RadioStation[]>([]);
  const [songs, setSongs] = useState<Song[]>([]);
  const [favorites, setFavorites] = useState<Favorite[]>([]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  const loadFavorites = useCallback(() => {
    fetch(`${API}/api/v1/favorites`)
      .then(r => r.json())
      .then(setFavorites)
      .catch(() => {});
  }, []);

  useEffect(loadFavorites, [loadFavorites]);

  const search = async () => {
    if (!query.trim()) return;
    setBusy(true);
    setMessage("");
    try {
      if (tab === "radio") {
        const r = await fetch(`${API}/api/v1/radio/search?name=${encodeURIComponent(query)}&limit=15`);
        setStations(await r.json());
      } else {
        const r = await fetch(`${API}/api/v1/music/search?q=${encodeURIComponent(query)}`);
        const data = await r.json();
        setSongs(Array.isArray(data) ? data : []);
        if (!Array.isArray(data)) setMessage("YouTube search failed — is the bridge container running?");
      }
    } catch {
      setMessage("Search failed — backend unreachable.");
    } finally {
      setBusy(false);
    }
  };

  const playRadio = async (name: string, url: string, position?: number) => {
    setBusy(true);
    setMessage("");
    try {
      const r = await fetch(`${API}/api/v1/devices/${deviceId}/play-radio`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(position !== undefined ? { name, url, position } : { name, url }),
      });
      if (r.ok) {
        onPlayed(name);
        onClose();
      } else {
        setMessage(`Play failed: ${(await r.json()).error || r.status}`);
      }
    } catch {
      setMessage("Play failed — backend unreachable.");
    } finally {
      setBusy(false);
    }
  };

  const playSong = async (s: Song) => {
    setBusy(true);
    setMessage("Preparing stream… (first play takes a few seconds)");
    try {
      const params = new URLSearchParams({ video_id: s.id, title: s.title, artist: s.artist });
      const r = await fetch(`${API}/api/v1/devices/${deviceId}/play-bridge?${params}`, { method: "POST" });
      if (r.ok) {
        onPlayed(`${s.artist} — ${s.title}`);
        onClose();
      } else {
        setMessage(`Play failed: ${(await r.json()).error || r.status}`);
      }
    } catch {
      setMessage("Play failed — backend unreachable.");
    } finally {
      setBusy(false);
    }
  };

  const addFavorite = (name: string, url: string) => {
    fetch(`${API}/api/v1/favorites`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, url }),
    }).then(loadFavorites);
  };

  const deleteFavorite = (id: number) => {
    fetch(`${API}/api/v1/favorites/${id}`, { method: "DELETE" }).then(loadFavorites);
  };

  const syncFavorites = async () => {
    setBusy(true);
    setMessage("Writing stations to device… (~4 s)");
    try {
      const r = await fetch(`${API}/api/v1/devices/${deviceId}/sync-favorites`, { method: "POST" });
      const data = await r.json();
      setMessage(r.ok ? `Synced ${data.synced} stations to device slots.` : `Sync failed: ${data.error}`);
    } catch {
      setMessage("Sync failed — backend unreachable.");
    } finally {
      setBusy(false);
    }
  };

  const isFavorite = (url: string) => favorites.some(f => f.url === url);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      style={{ background: "rgba(0,0,0,0.6)" }}
      onClick={onClose}
    >
      <div
        className="flex flex-col"
        style={{
          width: "min(440px, 95vw)",
          maxHeight: "85vh",
          borderRadius: 12,
          background: "linear-gradient(145deg, #f0f0f0 0%, #d8d8d8 100%)",
          border: "1px solid #bbb",
          boxShadow: "0 8px 32px rgba(0,0,0,0.45)",
          padding: 16,
        }}
        onClick={e => e.stopPropagation()}
      >
        {/* Header + tabs */}
        <div className="flex justify-between items-center mb-3">
          <div className="flex gap-2">
            <TabButton active={tab === "radio"} onClick={() => setTab("radio")}>Radio</TabButton>
            <TabButton active={tab === "youtube"} onClick={() => setTab("youtube")}>YouTube</TabButton>
          </div>
          <button onClick={onClose} style={{ fontSize: 18, color: "#555", padding: "0 6px" }} title="Close">✕</button>
        </div>

        {/* Search */}
        <div className="flex gap-2 mb-3">
          <input
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={e => e.key === "Enter" && search()}
            placeholder={tab === "radio" ? "Search stations (e.g. jazz, BBC…)" : "Search songs or artists…"}
            className="flex-1"
            style={{ padding: "8px 10px", borderRadius: 8, border: "1px solid #aaa", fontSize: 14, background: "#fff", color: "#222" }}
          />
          <button
            onClick={search}
            disabled={busy}
            style={{ padding: "8px 14px", borderRadius: 8, border: "1px solid #999", background: "#e5e5e5", fontSize: 14, color: "#333" }}
          >
            {busy ? "…" : "Search"}
          </button>
        </div>

        {message && <div style={{ fontSize: 12, color: "#666", marginBottom: 8 }}>{message}</div>}

        {/* Results */}
        <div className="overflow-y-auto" style={{ minHeight: 120 }}>
          {tab === "radio" && stations.map((s, i) => (
            <Row key={i}>
              <button className="flex-1 text-left" onClick={() => playRadio(s.name, s.url_resolved || s.url)} title="Play">
                <div style={{ fontSize: 14, color: "#222", fontWeight: 500 }}>{s.name}</div>
                <div style={{ fontSize: 11, color: "#777" }}>{s.country}{s.bitrate ? ` · ${s.bitrate} kbps` : ""}</div>
              </button>
              <button
                onClick={() => addFavorite(s.name, s.url_resolved || s.url)}
                title={isFavorite(s.url_resolved || s.url) ? "Already in favorites" : "Add to favorites"}
                style={{ fontSize: 18, color: isFavorite(s.url_resolved || s.url) ? "#eab308" : "#999", padding: "0 8px" }}
              >
                ★
              </button>
            </Row>
          ))}

          {tab === "youtube" && songs.map(s => (
            <Row key={s.id}>
              <button className="flex-1 text-left" onClick={() => playSong(s)} title="Play">
                <div style={{ fontSize: 14, color: "#222", fontWeight: 500 }}>{s.title}</div>
                <div style={{ fontSize: 11, color: "#777" }}>{s.artist}{s.duration ? ` · ${s.duration}` : ""}</div>
              </button>
            </Row>
          ))}
        </div>

        {/* Favorites (radio tab only) */}
        {tab === "radio" && (
          <div style={{ borderTop: "1px solid #bbb", marginTop: 10, paddingTop: 8 }}>
            <div className="flex justify-between items-center mb-1">
              <span style={{ fontSize: 12, fontWeight: 600, color: "#555", letterSpacing: 0.5 }}>
                FAVORITES ({favorites.length}/39)
              </span>
              <button
                onClick={syncFavorites}
                disabled={busy || favorites.length === 0}
                title="Write favorites into the device's station slots — physical next/prev buttons will browse them"
                style={{ fontSize: 11, padding: "4px 10px", borderRadius: 6, border: "1px solid #999", background: "#e5e5e5", color: "#333" }}
              >
                ⟳ Sync to device
              </button>
            </div>
            <div className="overflow-y-auto" style={{ maxHeight: 140 }}>
              {favorites.map((f, i) => (
                <Row key={f.id}>
                  <button className="flex-1 text-left" onClick={() => playRadio(f.name, f.url, i + 1)} title="Play">
                    <span style={{ fontSize: 13, color: "#222" }}>{f.name}</span>
                  </button>
                  <button onClick={() => deleteFavorite(f.id)} title="Remove" style={{ fontSize: 14, color: "#999", padding: "0 8px" }}>
                    ✕
                  </button>
                </Row>
              ))}
              {favorites.length === 0 && (
                <div style={{ fontSize: 12, color: "#888", padding: "6px 0" }}>
                  No favorites yet — search above and tap ★ to save stations.
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      style={{
        fontSize: 13,
        fontWeight: 600,
        padding: "5px 14px",
        borderRadius: 8,
        border: "1px solid #999",
        background: active ? "#555" : "#e5e5e5",
        color: active ? "#fff" : "#555",
      }}
    >
      {children}
    </button>
  );
}

function Row({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-center" style={{ padding: "6px 4px", borderBottom: "1px solid #ccc" }}>
      {children}
    </div>
  );
}

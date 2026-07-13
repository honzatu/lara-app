"use client";
import { useEffect, useState } from "react";
import LaraPanel from "./components/LaraPanel";

const API = process.env.NEXT_PUBLIC_API_URL || "";

interface Device {
  id: number;
  name: string;
  ip: string;
}

export default function Home() {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [activeId, setActiveId] = useState<number | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [name, setName] = useState("");
  const [ip, setIp] = useState("");
  const [error, setError] = useState("");

  const loadDevices = (selectId?: number) => {
    fetch(`${API}/api/v1/devices`)
      .then(r => r.json())
      .then((d: Device[]) => {
        const list = d || [];
        setDevices(list);
        setActiveId(prev => {
          if (selectId != null && list.some(x => x.id === selectId)) return selectId;
          if (prev != null && list.some(x => x.id === prev)) return prev;
          return list.length ? list[0].id : null;
        });
      })
      .catch(() => setDevices([]));
  };

  useEffect(() => loadDevices(), []);

  const addDevice = async () => {
    setError("");
    try {
      const r = await fetch(`${API}/api/v1/devices`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, ip }),
      });
      const data = await r.json();
      if (!r.ok) {
        setError(data.error || `HTTP ${r.status}`);
        return;
      }
      setName("");
      setIp("");
      setShowAdd(false);
      loadDevices(data.id);
    } catch {
      setError("Backend unreachable — is it running?");
    }
  };

  const removeDevice = async (id: number) => {
    await fetch(`${API}/api/v1/devices/${id}`, { method: "DELETE" });
    loadDevices();
  };

  const active = devices?.find(d => d.id === activeId) || null;
  const firstRun = devices !== null && devices.length === 0;

  return (
    <main
      className="min-h-screen flex flex-col items-center justify-center gap-5 p-4"
      style={{ background: "linear-gradient(135deg, #c8c8c8 0%, #e8e8e8 100%)" }}
    >
      {devices === null && <span style={{ color: "#555" }}>Loading…</span>}

      {/* Device switcher — one screen, pick which LARA is shown below */}
      {devices && devices.length > 0 && (
        <div className="flex flex-wrap items-center justify-center gap-2" style={{ maxWidth: 340 }}>
          {devices.map(d => (
            <button key={d.id} onClick={() => setActiveId(d.id)} style={pill(d.id === activeId)}>
              {d.name}
            </button>
          ))}
          <button
            onClick={() => { setShowAdd(s => !s); setError(""); }}
            title="Add another LARA"
            style={pill(false)}
          >
            ＋
          </button>
        </div>
      )}

      {/* The one selected device */}
      {active && <LaraPanel key={active.id} deviceId={active.id} deviceName={active.name} />}

      {active && devices && devices.length > 1 && (
        <button onClick={() => removeDevice(active.id)} style={removeBtn}>
          Remove “{active.name}”
        </button>
      )}

      {/* Add form — shown on first run or when the ＋ pill is toggled */}
      {(firstRun || showAdd) && (
        <div className="flex flex-col gap-3" style={card}>
          <span style={{ fontSize: 14, fontWeight: 600, color: "#444" }}>
            {firstRun ? "Add your first LARA" : "Add another LARA"}
          </span>
          <input
            value={name}
            onChange={e => setName(e.target.value)}
            placeholder="Name (e.g. Kitchen)"
            style={inputStyle}
          />
          <input
            value={ip}
            onChange={e => setIp(e.target.value)}
            placeholder="IP address (e.g. 192.168.1.50)"
            style={inputStyle}
          />
          {error && <span style={{ fontSize: 12, color: "#b91c1c" }}>{error}</span>}
          <button onClick={addDevice} disabled={!name || !ip} style={addButton}>
            Add device
          </button>
        </div>
      )}
    </main>
  );
}

function pill(active: boolean): React.CSSProperties {
  return {
    fontSize: 13,
    fontWeight: 600,
    padding: "6px 14px",
    borderRadius: 999,
    border: "1px solid #999",
    background: active ? "#555" : "#eee",
    color: active ? "#fff" : "#555",
    boxShadow: active ? "0 2px 6px rgba(0,0,0,0.25)" : "none",
    cursor: "pointer",
  };
}

const card: React.CSSProperties = {
  width: 320,
  borderRadius: 12,
  background: "linear-gradient(145deg, #f0f0f0 0%, #d8d8d8 100%)",
  border: "1px solid #bbb",
  boxShadow: "0 8px 32px rgba(0,0,0,0.35)",
  padding: 20,
};

const inputStyle: React.CSSProperties = {
  padding: "8px 10px",
  borderRadius: 8,
  border: "1px solid #aaa",
  fontSize: 14,
  background: "#fff",
  color: "#222",
};

const addButton: React.CSSProperties = {
  padding: 10,
  borderRadius: 8,
  border: "1px solid #999",
  background: "#555",
  color: "#fff",
  fontSize: 14,
  fontWeight: 600,
};

const removeBtn: React.CSSProperties = {
  fontSize: 11,
  color: "#888",
  background: "none",
  border: "none",
  cursor: "pointer",
};

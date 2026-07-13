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
  const [name, setName] = useState("");
  const [ip, setIp] = useState("");
  const [error, setError] = useState("");

  const loadDevices = () => {
    fetch(`${API}/api/v1/devices`)
      .then(r => r.json())
      .then(d => setDevices(d || []))
      .catch(() => setDevices([]));
  };

  useEffect(loadDevices, []);

  const addDevice = async () => {
    setError("");
    try {
      const r = await fetch(`${API}/api/v1/devices`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, ip }),
      });
      if (!r.ok) {
        setError((await r.json()).error || `HTTP ${r.status}`);
        return;
      }
      setName("");
      setIp("");
      loadDevices();
    } catch {
      setError("Backend unreachable — is it running on " + API + "?");
    }
  };

  return (
    <main
      className="min-h-screen flex flex-col items-center justify-center gap-6 p-4"
      style={{ background: "linear-gradient(135deg, #c8c8c8 0%, #e8e8e8 100%)" }}
    >
      {devices === null && <span style={{ color: "#555" }}>Loading…</span>}

      {devices !== null && devices.length === 0 && (
        <div
          className="flex flex-col gap-3"
          style={{
            width: 320,
            borderRadius: 12,
            background: "linear-gradient(145deg, #f0f0f0 0%, #d8d8d8 100%)",
            border: "1px solid #bbb",
            boxShadow: "0 8px 32px rgba(0,0,0,0.35)",
            padding: 20,
          }}
        >
          <span style={{ fontSize: 14, fontWeight: 600, color: "#444" }}>Add your first LARA device</span>
          <input
            value={name}
            onChange={e => setName(e.target.value)}
            placeholder="Name (e.g. Kitchen)"
            style={{ padding: "8px 10px", borderRadius: 8, border: "1px solid #aaa", fontSize: 14, background: "#fff", color: "#222" }}
          />
          <input
            value={ip}
            onChange={e => setIp(e.target.value)}
            placeholder="IP address (e.g. 192.168.1.50)"
            style={{ padding: "8px 10px", borderRadius: 8, border: "1px solid #aaa", fontSize: 14, background: "#fff", color: "#222" }}
          />
          {error && <span style={{ fontSize: 12, color: "#b91c1c" }}>{error}</span>}
          <button
            onClick={addDevice}
            disabled={!name || !ip}
            style={{ padding: "10px", borderRadius: 8, border: "1px solid #999", background: "#555", color: "#fff", fontSize: 14, fontWeight: 600 }}
          >
            Add device
          </button>
        </div>
      )}

      <div className="flex flex-wrap items-center justify-center gap-6">
        {devices?.map(d => (
          <LaraPanel key={d.id} deviceId={d.id} deviceName={d.name} />
        ))}
      </div>
    </main>
  );
}

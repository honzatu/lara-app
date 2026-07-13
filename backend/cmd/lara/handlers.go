// Copyright (c) 2026 honzatu. MIT License.
package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/honzatu/lara-app/internal/lms"
	"github.com/honzatu/lara-app/internal/protocol"
)

// resolveStreamURL follows M3U playlists to the actual audio stream URL.
// TuneIn returns audio/x-mpegurl with a list of URLs — last one is the real stream.
func resolveStreamURL(rawURL string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("User-Agent", "LARA-App/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return rawURL, err
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "mpegurl") && !strings.Contains(ct, "m3u") {
		return rawURL, nil // already a direct stream
	}

	body, _ := io.ReadAll(resp.Body)
	resolved := rawURL
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http") {
			resolved = line // take last http URL = actual stream (not pre-roll ad)
		}
	}
	return resolved, nil
}

// proxyStream fetches a stream URL (resolving M3U playlists first) and pipes
// the audio to the client. withICY requests inline ICY metadata — wanted by
// the browser audio analyzer, but NEVER for the LARA (metadata bytes would
// corrupt plain MP3 playback).
func proxyStream(w http.ResponseWriter, streamURL string, withICY bool) {
	resolved, err := resolveStreamURL(streamURL)
	if err != nil {
		http.Error(w, "resolve error: "+err.Error(), http.StatusBadGateway)
		return
	}

	req, err := http.NewRequest("GET", resolved, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "LARA-App/1.0")
	if withICY {
		req.Header.Set("Icy-MetaData", "1")
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)
}

// handleStreamRadio proxies an internet radio stream for the browser audio analyzer.
func handleStreamRadio(w http.ResponseWriter, r *http.Request) {
	streamURL := r.URL.Query().Get("url")
	if streamURL == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	proxyStream(w, streamURL, true)
}

// handleShortStream serves /s?k=<token> — the LARA-facing stream proxy.
// The LARA can only fetch short plain-HTTP URLs (station slot fits 69 chars,
// no TLS), so https/long stream URLs are aliased to short tokens and fetched
// through here.
func handleShortStream(w http.ResponseWriter, r *http.Request) {
	streamURL, ok := store.URLForToken(r.URL.Query().Get("k"))
	if !ok {
		http.Error(w, "unknown stream token", http.StatusNotFound)
		return
	}
	proxyStream(w, streamURL, false)
}

// deviceStreamURL returns the URL to write into a LARA station slot.
// HTTPS and over-long URLs are routed through the backend's /s proxy;
// this requires LAN_HOST (the docker host's LAN IP) to be configured.
func deviceStreamURL(rawURL string) string {
	if !strings.HasPrefix(rawURL, "https://") && len(rawURL) < 80 {
		return rawURL
	}
	host := os.Getenv("LAN_HOST")
	if host == "" {
		return rawURL // not configured — pass through unchanged
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8400"
	}
	return "http://" + host + ":" + port + "/s?k=" + store.AliasURL(rawURL)
}

// lmsEnabled reports whether the optional Squeezebox / LMS integration is
// turned on. It is OFF by default: the direct binary protocol is the primary
// (and recommended) way to control LARA. Set ENABLE_LMS=true only if you run
// the optional LMS container and want Audio Zone / multi-room mode.
func lmsEnabled() bool {
	v := os.Getenv("ENABLE_LMS")
	return v == "true" || v == "1"
}

func lmsClient() *lms.Client {
	host := os.Getenv("LMS_HOST")
	if host == "" { host = "localhost" }
	port := os.Getenv("LMS_CLI_PORT")
	if port == "" { port = "9090" }
	return lms.NewClient(host, port, "admin", os.Getenv("LARA_PASS"))
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func lara(ip string) *protocol.Client {
	return protocol.NewClient(ip, os.Getenv("LARA_PASS"))
}

// handlePlay resumes or starts last known stream directly via LMS
// LMS Docker has DNS (8.8.8.8) so it can resolve stream hostnames
func handlePlay(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	d, ok := store.DeviceByID(id)
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	if lmsEnabled() && d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			info, _ := c.GetStatus(d.MAC)
			if info.CurrentTitle != "" || info.Title != "" {
				c.Play(d.MAC)
				_, name := store.GetLastStream(id)
				jsonOK(w, map[string]string{"status": "playing", "name": name})
				return
			}
			streamURL, name := store.GetLastStream(id)
			c.PlayURL(d.MAC, streamURL)
			jsonOK(w, map[string]string{"status": "playing", "url": streamURL, "name": name})
			return
		}
	}
	// Fallback: binary protocol
	streamURL, name := store.GetLastStream(id)
	if err := lara(d.IP).LaraPlayStream(deviceStreamURL(streamURL), name); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	store.SetPlaying(id, true)
	jsonOK(w, map[string]string{"status": "playing", "url": streamURL, "name": name})
}

func handleGetDevices(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, store.AllDevices())
}

func handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		IP   string `json:"ip"`
		MAC  string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.IP == "" {
		jsonErr(w, 400, "name and ip required")
		return
	}
	d, err := store.CreateDevice(req.Name, req.IP, req.MAC)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, d)
}

func handleGetDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	jsonOK(w, d)
}

func handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := store.DeleteDevice(mux.Vars(r)["id"]); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

func handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	d, ok := store.DeviceByID(id)
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	// Use LMS status when device has MAC — more accurate for Audio Zone mode
	if lmsEnabled() && d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			info, err := c.GetStatus(d.MAC)
			if err == nil {
				title := info.CurrentTitle
				if strings.HasPrefix(title, "http") || title == "" {
					title = info.Title
				}
				if strings.HasPrefix(title, "http") || title == "" {
					title = info.Artist
				}
				if strings.HasPrefix(title, "http") {
					title = ""
				}
				_, storedName := store.GetLastStream(id)
				stationName := info.StreamTitle
				if stationName == "" {
					stationName = storedName
				}
				jsonOK(w, map[string]any{
					"playing":      info.Mode == "play",
					"volume":       info.Volume,
					"track_title":  title,
					"station_name": stationName,
					"artist":       info.Artist,
					"elapsed":      info.Elapsed,
					"duration":     info.Duration,
					"stream_url":   info.StreamURL,
				})
				return
			}
		}
	}
	// Fallback: TCP reachability + last commanded state. The LARA "status"
	// packet is the PLAY command — polling it would start playback on a
	// stopped device, so we never query the device here.
	conn, err := net.DialTimeout("tcp", d.IP+":80", 1500*time.Millisecond)
	if err != nil {
		jsonErr(w, 503, "lara unreachable")
		return
	}
	conn.Close()
	_, storedName := store.GetLastStream(id)
	jsonOK(w, map[string]any{
		"playing":      store.IsPlaying(id),
		"volume":       store.GetVolume(id),
		"station_name": storedName,
		"track_title":  "",
		"artist":       "",
		"elapsed":      0,
		"duration":     0,
	})
}

func handlePlayRadio(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	var req struct {
		URL      string `json:"url"`
		Name     string `json:"name"`
		Position *int   `json:"position"` // favorites slot 1–39; omit for new/unknown stations
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		jsonErr(w, 400, "url required")
		return
	}
	id := mux.Vars(r)["id"]
	// Use LMS when device has MAC (Audio Zone enabled)
	if lmsEnabled() && d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			if err := c.PlayURL(d.MAC, req.URL); err != nil {
				jsonErr(w, 502, err.Error())
				return
			}
			store.SetLastStream(id, req.URL, req.Name)
			jsonOK(w, map[string]string{"status": "playing"})
			return
		}
	}
	// Binary protocol: write URL to slot 0, keep synced favorites in slots 1+
	position := -1
	if req.Position != nil {
		position = *req.Position
	}
	if err := lara(d.IP).PlayRadioAt(deviceStreamURL(req.URL), smartTruncateName(req.Name), position); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	store.SetLastStream(id, req.URL, req.Name)
	store.SetPlaying(id, true)
	jsonOK(w, map[string]string{"status": "playing"})
}

func handlePause(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	if lmsEnabled() && d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			c.Pause(d.MAC)
		}
	}
	// Binary stop silences LARA hardware
	lara(d.IP).Stop()
	store.SetPlaying(mux.Vars(r)["id"], false)
	jsonOK(w, map[string]string{"status": "paused"})
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	// Stop LMS first — this actually stops the Squeezebox stream to LARA.
	// Binary STOP alone is not enough: LMS keeps pushing audio via Squeezebox protocol.
	if lmsEnabled() && d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			c.Stop(d.MAC)
		}
	}
	// Binary STOP as well for hardware silence fallback
	lara(d.IP).Stop()
	store.SetPlaying(mux.Vars(r)["id"], false)
	jsonOK(w, map[string]string{"status": "stopped"})
}

func handleVolume(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	var req struct {
		Volume int `json:"volume"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, 400, "volume required")
		return
	}
	// Sync volume to LMS so status polling returns the new value (not the old one)
	if lmsEnabled() && d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			c.SetVolume(d.MAC, req.Volume)
		}
	}
	if err := lara(d.IP).SetVolume(req.Volume); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	store.SetVolume(mux.Vars(r)["id"], req.Volume)
	jsonOK(w, map[string]int{"volume": req.Volume})
}

func handleSkip(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	if lmsEnabled() && d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			if err := c.Skip(d.MAC); err != nil {
				jsonErr(w, 502, err.Error())
				return
			}
			jsonOK(w, map[string]string{"status": "ok"})
			return
		}
	}
	// Fallback: binary protocol
	if err := lara(d.IP).Next(); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

func handlePrev(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	if lmsEnabled() && d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			if err := c.Prev(d.MAC); err != nil {
				jsonErr(w, 502, err.Error())
				return
			}
			jsonOK(w, map[string]string{"status": "ok"})
			return
		}
	}
	// Fallback: binary protocol
	if err := lara(d.IP).Prev(); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

func handleMute(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	d, ok := store.DeviceByID(id)
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	// Use tracked volume — querying the device would send a PLAY packet
	// (the protocol has no read-only status) and start playback when muted.
	c := lara(d.IP)
	current := store.GetVolume(id)
	if current > 0 {
		store.SetMuteVolume(id, current)
		if err := c.SetVolume(0); err != nil {
			jsonErr(w, 502, err.Error())
			return
		}
		store.SetVolume(id, 0)
	} else {
		prev := store.GetMuteVolume(id)
		if prev == 0 {
			prev = 50
		}
		if err := c.SetVolume(prev); err != nil {
			jsonErr(w, 502, err.Error())
			return
		}
		store.SetVolume(id, prev)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

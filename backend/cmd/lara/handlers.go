// Copyright (c) 2026 honzatu. MIT License.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"github.com/honzatu/lara-app/internal/lms"
	"github.com/honzatu/lara-app/internal/protocol"
)

// handleStreamRadio proxies an internet radio stream locally so LARA can reach it.
// LARA fetches http://192.168.1.3:8400/stream/radio?url=... from local network.
// We fetch from internet and forward the audio bytes to LARA.
func handleStreamRadio(w http.ResponseWriter, r *http.Request) {
	streamURL := r.URL.Query().Get("url")
	if streamURL == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	req, err := http.NewRequest("GET", streamURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "LARA-App/1.0")
	req.Header.Set("Icy-MetaData", "1")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward content-type (audio/mpeg etc.)
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "audio/mpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	io.Copy(w, resp.Body)
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
	streamURL, name := store.GetLastStream(id)
	if d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			c.PlayURL(d.MAC, streamURL)
		}
	} else {
		lara(d.IP).LaraPlayStream(streamURL, name)
	}
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

func handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	// Use LMS status when device has MAC — more accurate for Audio Zone mode
	if d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			info, err := c.GetStatus(d.MAC)
			if err == nil {
				jsonOK(w, map[string]any{
					"playing":       info.Mode == "play",
					"volume":        info.Volume,
					"station_index": 0,
					"track_title":   info.CurrentTitle,
				})
				return
			}
		}
	}
	// Fallback: binary protocol status
	status, err := lara(d.IP).GetStatus()
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOK(w, map[string]any{
		"playing":       status.Playing,
		"volume":        status.Volume,
		"station_index": status.StationIndex,
		"track_title":   "",
	})
}

func handlePlayRadio(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	var req struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		jsonErr(w, 400, "url required")
		return
	}
	id := mux.Vars(r)["id"]
	// Use LMS when device has MAC (Audio Zone enabled)
	if d.MAC != "" {
		c := lmsClient()
		if err := c.Connect(); err == nil {
			defer c.Close()
			// Proxy via local backend so LARA can reach the stream
			c.SetVolume(d.MAC, 60)
			if err := c.PlayURL(d.MAC, req.URL); err != nil {
				jsonErr(w, 502, err.Error())
				return
			}
			store.SetLastStream(id, req.URL, req.Name)
			jsonOK(w, map[string]string{"status": "playing"})
			return
		}
	}
	// Fallback: binary protocol
	if err := lara(d.IP).LaraPlayStream(req.URL, req.Name); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	store.SetLastStream(id, req.URL, req.Name)
	jsonOK(w, map[string]string{"status": "playing"})
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	if err := lara(d.IP).Stop(); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
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
	if err := lara(d.IP).SetVolume(req.Volume); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOK(w, map[string]int{"volume": req.Volume})
}

func handleSkip(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
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
	c := lara(d.IP)
	status, err := c.GetStatus()
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	if status.Volume > 0 {
		store.SetMuteVolume(id, status.Volume)
		c.SetVolume(0)
	} else {
		prev := store.GetMuteVolume(id)
		if prev == 0 {
			prev = 50
		}
		c.SetVolume(prev)
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

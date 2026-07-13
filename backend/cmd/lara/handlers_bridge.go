// Copyright (c) 2026 honzatu. MIT License.
// Music bridge integration — YouTube Music playback on LARA hardware.
//
// The bridge (see bridge/main.py) downloads audio with yt-dlp, transcodes it
// with ffmpeg to a plain MP3 stream and serves it on the LAN. LARA can only
// play plain HTTP streams, so the backend writes the bridge's LAN URL into
// a LARA station slot and sends PLAY.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// bridgeInternalURL — bridge address reachable from this backend
// (inside docker compose that is the service name).
func bridgeInternalURL() string {
	if v := os.Getenv("BRIDGE_INTERNAL_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://bridge:8282"
}

// bridgeLanURL — bridge address reachable from the physical LARA device.
// LARA lives on your LAN, outside the docker network, so this must be the
// docker host's LAN IP, e.g. http://192.168.1.10:8282 — set BRIDGE_LAN_URL.
func bridgeLanURL() string {
	return strings.TrimRight(os.Getenv("BRIDGE_LAN_URL"), "/")
}

// Active bridge sessions per device (device id → bridge session UUID)
var (
	bridgeSessions   = map[string]string{}
	bridgeSessionsMu sync.Mutex
)

// handleMusicProxy forwards /api/v1/music/* to the bridge (search, playlists,
// moods, …). GET only.
func handleMusicProxy(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/api/v1/music")
	targetURL := bridgeInternalURL() + "/api/music" + subPath
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}
	proxyGet(w, targetURL)
}

// handleNowPlaying forwards to the bridge's ICY metadata reader:
// /api/v1/radio/now-playing?url=<stream-url> → current StreamTitle.
func handleNowPlaying(w http.ResponseWriter, r *http.Request) {
	targetURL := bridgeInternalURL() + "/api/radio/now-playing"
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}
	proxyGet(w, targetURL)
}

func proxyGet(w http.ResponseWriter, targetURL string) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(targetURL)
	if err != nil {
		jsonErr(w, http.StatusServiceUnavailable, "music bridge unavailable — is the bridge container running?")
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handlePlayBridge plays a YouTube Music track on a LARA device:
//  1. stop any previous bridge session + binary STOP (a playing LARA treats
//     PLAY as a status query, so it must be stopped first)
//  2. warmup — bridge pre-downloads the audio so LARA gets data instantly
//  3. create a bridge session and queue the track
//  4. write the session's LAN stream URL into LARA and send PLAY
//
// POST /api/v1/devices/{id}/play-bridge?video_id=X&title=Y&artist=Z
func handlePlayBridge(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	d, ok := store.DeviceByID(id)
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	if bridgeLanURL() == "" {
		jsonErr(w, 500, "BRIDGE_LAN_URL is not set — LARA cannot reach the bridge without it (see .env.example)")
		return
	}

	videoID := r.URL.Query().Get("video_id")
	title := r.URL.Query().Get("title")
	artist := r.URL.Query().Get("artist")
	if videoID == "" {
		jsonErr(w, 400, "video_id required")
		return
	}
	if title == "" {
		title = "YT Music"
	}

	// 1. Stop previous session + hardware STOP
	bridgeSessionsMu.Lock()
	oldSession := bridgeSessions[id]
	bridgeSessionsMu.Unlock()
	if oldSession != "" {
		client := &http.Client{Timeout: 4 * time.Second}
		if resp, err := client.Post(bridgeInternalURL()+"/api/session/"+oldSession+"/stop", "application/json", nil); err == nil {
			resp.Body.Close()
		}
		bridgeSessionsMu.Lock()
		delete(bridgeSessions, id)
		bridgeSessionsMu.Unlock()
	}
	lara(d.IP).Stop()
	time.Sleep(400 * time.Millisecond)

	// 2. Warmup — pre-download so the stream starts instantly for LARA
	warmupClient := &http.Client{Timeout: 90 * time.Second}
	warmupResp, err := warmupClient.Get(bridgeInternalURL() + "/api/warmup?video_id=" + videoID)
	if err != nil {
		jsonErr(w, http.StatusServiceUnavailable, "music bridge unavailable")
		return
	}
	warmupBody, _ := io.ReadAll(warmupResp.Body)
	warmupResp.Body.Close()
	if warmupResp.StatusCode != http.StatusOK {
		jsonErr(w, http.StatusBadGateway, "video unavailable: "+strings.TrimSpace(string(warmupBody)))
		return
	}

	// 3. Create session + queue track
	client := &http.Client{Timeout: 8 * time.Second}
	createResp, err := client.Post(bridgeInternalURL()+"/api/session/create", "application/json", nil)
	if err != nil {
		jsonErr(w, http.StatusServiceUnavailable, "bridge session create failed")
		return
	}
	var sessionData struct {
		SessionID string `json:"sessionId"`
	}
	json.NewDecoder(createResp.Body).Decode(&sessionData)
	createResp.Body.Close()
	if sessionData.SessionID == "" {
		jsonErr(w, 500, "bridge returned empty session id")
		return
	}

	trackPayload, _ := json.Marshal([]map[string]string{
		{"videoId": videoID, "title": title, "artist": artist},
	})
	addResp, err := client.Post(
		bridgeInternalURL()+"/api/session/"+sessionData.SessionID+"/add",
		"application/json", bytes.NewBuffer(trackPayload),
	)
	if err != nil {
		jsonErr(w, http.StatusServiceUnavailable, "bridge session add failed")
		return
	}
	addResp.Body.Close()

	bridgeSessionsMu.Lock()
	bridgeSessions[id] = sessionData.SessionID
	bridgeSessionsMu.Unlock()

	// 4. Point LARA at the session stream and play
	streamURL := bridgeLanURL() + "/stream/session?id=" + sessionData.SessionID
	if err := lara(d.IP).LaraPlayStream(streamURL, smartTruncateName(title)); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	store.SetLastStream(id, streamURL, title)
	jsonOK(w, map[string]any{
		"status":    "playing",
		"sessionId": sessionData.SessionID,
	})
}

// handleBridgeSession relays session commands (skip/prev/seek/stop/status/add)
// to the bridge for the device's active session.
// POST/GET /api/v1/devices/{id}/session/{action}
func handleBridgeSession(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	action := vars["action"]

	bridgeSessionsMu.Lock()
	sessionID := bridgeSessions[id]
	bridgeSessionsMu.Unlock()
	if sessionID == "" {
		jsonErr(w, 404, "no active session")
		return
	}

	client := &http.Client{Timeout: 8 * time.Second}
	base := bridgeInternalURL() + "/api/session/" + sessionID + "/"

	var resp *http.Response
	var err error
	switch action {
	case "skip", "prev", "stop":
		resp, err = client.Post(base+action, "application/json", nil)
		if action == "stop" && err == nil {
			bridgeSessionsMu.Lock()
			delete(bridgeSessions, id)
			bridgeSessionsMu.Unlock()
		}
	case "seek", "add":
		resp, err = client.Post(base+action, "application/json", r.Body)
	case "status":
		resp, err = client.Get(base + "status")
	default:
		jsonErr(w, 400, "unknown session action")
		return
	}
	if err != nil {
		jsonErr(w, http.StatusServiceUnavailable, "music bridge unavailable")
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

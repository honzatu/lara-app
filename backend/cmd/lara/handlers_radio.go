// Copyright (c) 2026 honzatu. MIT License.
// Radio station search (radio-browser.info), favorites and slot sync.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/honzatu/lara-app/internal/protocol"
)

// handleRadioSearch proxies station search to the community Radio Browser API
// (https://www.radio-browser.info — 45k+ stations, no API key needed).
// Query params: name, country (2-letter ISO), tag, limit (default 20).
func handleRadioSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := q.Get("limit")
	if limit == "" {
		limit = "20"
	}

	apiURL := "https://de1.api.radio-browser.info/json/stations/search" +
		"?order=votes&reverse=true&hidebroken=true&limit=" + url.QueryEscape(limit)
	if name := q.Get("name"); name != "" {
		apiURL += "&name=" + url.QueryEscape(name)
	}
	if country := strings.ToUpper(q.Get("country")); country != "" {
		apiURL += "&countrycode=" + url.QueryEscape(country)
	}
	if tag := q.Get("tag"); tag != "" {
		apiURL += "&tag=" + url.QueryEscape(tag)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "LARA-App/1.0")

	resp, err := client.Do(req)
	if err != nil {
		jsonErr(w, http.StatusServiceUnavailable, "radio browser unavailable")
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ── Favorites CRUD ───────────────────────────────────────────────────

func handleGetFavorites(w http.ResponseWriter, r *http.Request) {
	favs := store.AllFavorites()
	if favs == nil {
		favs = []Favorite{}
	}
	jsonOK(w, favs)
}

func handleAddFavorite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.URL == "" {
		jsonErr(w, 400, "name and url required")
		return
	}
	f, err := store.AddFavorite(req.Name, req.URL)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
	jsonOK(w, f)
}

func handleDeleteFavorite(w http.ResponseWriter, r *http.Request) {
	if err := store.DeleteFavorite(mux.Vars(r)["id"]); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// ── Sync favorites into physical LARA slots ──────────────────────────

// handleSyncFavorites writes the favorites list (A–Z) into the LARA station
// slots so the physical next/prev buttons and the device menu browse them.
// Slot 0 stays reserved for whatever is currently playing.
func handleSyncFavorites(w http.ResponseWriter, r *http.Request) {
	d, ok := store.DeviceByID(mux.Vars(r)["id"])
	if !ok {
		jsonErr(w, 404, "not found")
		return
	}
	favs := store.AllFavorites()
	streams := make([]protocol.NamedStream, 0, len(favs))
	for _, f := range favs {
		streams = append(streams, protocol.NamedStream{
			Name: smartTruncateName(f.Name),
			URL:  deviceStreamURL(f.URL), // https/long URLs go through the /s proxy
		})
	}
	synced, err := lara(d.IP).SyncStations(streams)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOK(w, map[string]int{"synced": synced})
}

// smartTruncateName shortens a station name to the 12 characters that fit a
// LARA slot, dropping filler words ("Radio", bitrate suffixes) first so the
// distinctive part of the name survives.
func smartTruncateName(name string) string {
	suffixes := []string{" - Radio", " - Rádio", " - CZ", " - 128", " - MP3", " - 64", " - 320"}
	for _, suf := range suffixes {
		if idx := strings.Index(strings.ToLower(name), strings.ToLower(suf)); idx > 0 {
			name = name[:idx]
		}
	}

	words := strings.Fields(strings.TrimSpace(name))
	var kept []string
	for _, word := range words {
		lower := strings.ToLower(word)
		if lower != "radio" && lower != "rádio" {
			kept = append(kept, word)
		}
	}
	name = strings.TrimSpace(strings.Join(kept, " "))
	if len(name) < 3 {
		name = strings.Split(strings.TrimSpace(name), " ")[0]
		if name == "" {
			return "Radio"
		}
	}

	if runes := []rune(name); len(runes) > 12 {
		name = string(runes[:12])
	}
	return name
}

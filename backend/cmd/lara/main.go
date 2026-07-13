// Copyright (c) 2026 honzatu. MIT License.
// LARA App — standalone controller for LARA Radio/Intercom hardware (ELKO EP)
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	if err := initStore(); err != nil {
		log.Fatalf("[LARA] DB init failed: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8400"
	}

	r := mux.NewRouter()

	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"lara-app","version":"0.1.0"}`)
	}).Methods("GET")

	// Simple test page — plain HTML, no React
	r.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html><html><body style="font-family:sans-serif;padding:20px;background:#222;color:#fff">
<h2>LARA Test</h2>
<button id="play" style="padding:16px 32px;font-size:18px;background:#22c55e;color:#fff;border:none;border-radius:8px;cursor:pointer">▶ PLAY</button>
<button id="stop" style="padding:16px 32px;font-size:18px;background:#ef4444;color:#fff;border:none;border-radius:8px;cursor:pointer;margin-left:12px">⏹ STOP</button>
<div id="log" style="margin-top:20px;font-family:monospace;font-size:14px"></div>
<script>
function log(msg) { document.getElementById('log').innerHTML += '<div>' + new Date().toLocaleTimeString() + ': ' + msg + '</div>'; }
document.getElementById('play').onclick = function() {
  log('Clicking PLAY...');
  fetch('/api/v1/devices/1/play', {method:'POST', headers:{'Content-Type':'application/json'}})
    .then(r => r.json()).then(d => log('OK: ' + JSON.stringify(d))).catch(e => log('ERR: ' + e));
};
document.getElementById('stop').onclick = function() {
  log('Clicking STOP...');
  fetch('/api/v1/devices/1/stop', {method:'POST', headers:{'Content-Type':'application/json'}})
    .then(r => r.json()).then(d => log('OK: ' + JSON.stringify(d))).catch(e => log('ERR: ' + e));
};
log('Page loaded. Click PLAY to test.');
</script></body></html>`)
	}).Methods("GET")

	// API v1
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/devices", handleGetDevices).Methods("GET")
	api.HandleFunc("/devices", handleCreateDevice).Methods("POST")
	api.HandleFunc("/devices/{id}", handleGetDevice).Methods("GET")
	api.HandleFunc("/devices/{id}/status", handleDeviceStatus).Methods("GET")
	api.HandleFunc("/devices/{id}/play", handlePlay).Methods("POST")
	api.HandleFunc("/devices/{id}/play-radio", handlePlayRadio).Methods("POST")
	api.HandleFunc("/devices/{id}/pause", handlePause).Methods("POST")
	api.HandleFunc("/devices/{id}/stop", handleStop).Methods("POST")
	api.HandleFunc("/devices/{id}/volume", handleVolume).Methods("POST")
	api.HandleFunc("/devices/{id}/skip", handleSkip).Methods("POST")
	api.HandleFunc("/devices/{id}/prev", handlePrev).Methods("POST")
	api.HandleFunc("/devices/{id}/mute", handleMute).Methods("POST")

	// Radio stream proxy — LARA fetches this locally, we proxy to internet
	r.HandleFunc("/stream/radio", handleStreamRadio).Methods("GET")

	// WebSocket
	r.HandleFunc("/ws", handleWebSocket)

	// CORS wrapper — must wrap the entire mux, not use r.Use()
	// gorilla/mux r.Use() skips middleware for unmatched routes (OPTIONS returns 404)
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Skip logging for frequent polling endpoints to keep logs readable
		if req.URL.Path != "/health" && !strings.HasSuffix(req.URL.Path, "/status") {
			log.Printf("[REQ] %s %s from %s", req.Method, req.URL.Path, req.RemoteAddr)
		}
		r.ServeHTTP(w, req)
	})

	log.Printf("[LARA] Starting on :%s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatal(err)
	}
}

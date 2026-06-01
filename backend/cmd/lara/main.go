// Copyright (c) 2026 honzatu. MIT License.
// LARA App — standalone controller for LARA Radio/Intercom hardware (ELKO EP)
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

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

	// CORS — allow all origins (local network app)
	cors := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if req.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
	r.Use(cors)
	// Handle OPTIONS preflight for all routes
	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, req)
	}).Methods(http.MethodOptions)

	// Health check
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"lara-app","version":"0.1.0"}`)
	}).Methods("GET")

	// API v1
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/devices", handleGetDevices).Methods("GET")
	api.HandleFunc("/devices", handleCreateDevice).Methods("POST")
	api.HandleFunc("/devices/{id}", handleGetDevice).Methods("GET")
	api.HandleFunc("/devices/{id}/status", handleDeviceStatus).Methods("GET")
	api.HandleFunc("/devices/{id}/play", handlePlay).Methods("POST")
	api.HandleFunc("/devices/{id}/play-radio", handlePlayRadio).Methods("POST")
	api.HandleFunc("/devices/{id}/stop", handleStop).Methods("POST")
	api.HandleFunc("/devices/{id}/volume", handleVolume).Methods("POST")
	api.HandleFunc("/devices/{id}/skip", handleSkip).Methods("POST")
	api.HandleFunc("/devices/{id}/prev", handlePrev).Methods("POST")
	api.HandleFunc("/devices/{id}/mute", handleMute).Methods("POST")

	// WebSocket
	r.HandleFunc("/ws", handleWebSocket)

	log.Printf("[LARA] Starting on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}

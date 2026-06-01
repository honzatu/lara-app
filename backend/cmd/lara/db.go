// Copyright (c) 2026 honzatu. MIT License.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// Device represents a LARA hardware device
type Device struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	IP   string `json:"ip"`
	MAC  string `json:"mac"`
}

// Store manages devices in SQLite + runtime state in memory
type Store struct {
	db         *sql.DB
	mu         sync.RWMutex
	muteVolume map[string]int    // device id → volume before mute
	lastStream map[string]string // device id → last played stream URL
	lastName   map[string]string // device id → last played station name
}

var store *Store

func initStore() error {
	path := os.Getenv("DB_PATH")
	if path == "" {
		path = "/data/lara.db"
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS devices (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			ip   TEXT NOT NULL UNIQUE,
			mac  TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	store = &Store{
		db:         db,
		muteVolume: make(map[string]int),
		lastStream: make(map[string]string),
		lastName:   make(map[string]string),
	}
	return nil
}

func (s *Store) AllDevices() []Device {
	rows, err := s.db.Query("SELECT id, name, ip, mac FROM devices ORDER BY id")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var devices []Device
	for rows.Next() {
		var d Device
		rows.Scan(&d.ID, &d.Name, &d.IP, &d.MAC)
		devices = append(devices, d)
	}
	return devices
}

func (s *Store) DeviceByID(id string) (Device, bool) {
	var d Device
	err := s.db.QueryRow("SELECT id, name, ip, mac FROM devices WHERE id = ?", id).
		Scan(&d.ID, &d.Name, &d.IP, &d.MAC)
	return d, err == nil
}

func (s *Store) CreateDevice(name, ip, mac string) (Device, error) {
	res, err := s.db.Exec("INSERT INTO devices (name, ip, mac) VALUES (?, ?, ?)", name, ip, mac)
	if err != nil {
		return Device{}, err
	}
	id, _ := res.LastInsertId()
	return Device{ID: int(id), Name: name, IP: ip, MAC: mac}, nil
}

func (s *Store) SetLastStream(id, url, name string) {
	s.mu.Lock()
	s.lastStream[id] = url
	s.lastName[id] = name
	s.mu.Unlock()
}

func (s *Store) GetLastStream(id string) (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	url := s.lastStream[id]
	if url == "" {
		url = "http://icecast3.play.cz/impuls128.mp3" // default
	}
	name := s.lastName[id]
	if name == "" {
		name = "Impuls"
	}
	return url, name
}

func (s *Store) SetMuteVolume(id string, vol int) {
	s.mu.Lock()
	s.muteVolume[id] = vol
	s.mu.Unlock()
}

func (s *Store) GetMuteVolume(id string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.muteVolume[id]
}

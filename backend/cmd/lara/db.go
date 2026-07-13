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
	muteVolume map[string]int // device id → volume before mute
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
	// Persist last played stream URL + name across restarts
	if _, err := db.Exec(`
		ALTER TABLE devices ADD COLUMN last_stream_url  TEXT NOT NULL DEFAULT '';
		ALTER TABLE devices ADD COLUMN last_stream_name TEXT NOT NULL DEFAULT ''
	`); err != nil {
		// Columns already exist — ignore
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS favorites (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url  TEXT NOT NULL UNIQUE
		)
	`); err != nil {
		return fmt.Errorf("migrate favorites: %w", err)
	}
	store = &Store{
		db:         db,
		muteVolume: make(map[string]int),
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
	s.db.Exec(
		"UPDATE devices SET last_stream_url=?, last_stream_name=? WHERE id=?",
		url, name, id,
	)
}

func (s *Store) GetLastStream(id string) (string, string) {
	var url, name string
	s.db.QueryRow(
		"SELECT last_stream_url, last_stream_name FROM devices WHERE id=?", id,
	).Scan(&url, &name)
	if url == "" {
		url = "http://icecast.rozhlas.cz/radiozurnal_mp3_128.mp3"
	}
	if name == "" {
		name = "Radiozurnal"
	}
	return url, name
}

// Favorite is a saved radio station
type Favorite struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (s *Store) AllFavorites() []Favorite {
	rows, err := s.db.Query("SELECT id, name, url FROM favorites ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var favs []Favorite
	for rows.Next() {
		var f Favorite
		rows.Scan(&f.ID, &f.Name, &f.URL)
		favs = append(favs, f)
	}
	return favs
}

func (s *Store) AddFavorite(name, url string) (Favorite, error) {
	res, err := s.db.Exec("INSERT INTO favorites (name, url) VALUES (?, ?)", name, url)
	if err != nil {
		return Favorite{}, err
	}
	id, _ := res.LastInsertId()
	return Favorite{ID: int(id), Name: name, URL: url}, nil
}

func (s *Store) DeleteFavorite(id string) error {
	_, err := s.db.Exec("DELETE FROM favorites WHERE id = ?", id)
	return err
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

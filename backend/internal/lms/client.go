// Copyright (c) 2026 honzatu. MIT License.
// LMS (Logitech Media Server) CLI client — Squeezebox protocol
package lms

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client connects to LMS CLI port (default 9090) and controls players
type Client struct {
	host     string
	port     string
	user     string
	pass     string
	conn     net.Conn
	mu       sync.Mutex
	scanner  *bufio.Scanner
}

// PlayerInfo holds basic info about a connected Squeezebox/LARA player
type PlayerInfo struct {
	MAC          string
	Name         string
	IP           string
	Model        string
	Connected    bool
	Power        int
	Volume       int
	Mode         string  // play, pause, stop
	CurrentTitle string  // ICY metadata title (radio streams)
	Title        string  // track title (local files / YT Music)
	Artist       string  // track artist
	StreamTitle  string  // LMS playlist/station title (name of radio station)
	StreamURL    string  // current stream URL (for browser audio analyzer)
	Elapsed      float64 // playback position in seconds
	Duration     float64 // track duration in seconds
}

// NewClient creates a new LMS CLI client
func NewClient(host, port, user, pass string) *Client {
	return &Client{
		host: host,
		port: port,
		user: user,
		pass: pass,
	}
}

// Connect establishes TCP connection to LMS CLI and authenticates
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%s", c.host, c.port), 5*time.Second)
	if err != nil {
		return fmt.Errorf("LMS connect failed: %w", err)
	}
	c.conn = conn
	c.scanner = bufio.NewScanner(conn)

	// Authenticate
	if c.user != "" {
		_, err = fmt.Fprintf(c.conn, "login %s %s\n", c.user, c.pass)
		if err != nil {
			return err
		}
		c.scanner.Scan() // consume "login ok"
	}
	return nil
}

// Close disconnects from LMS
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// IsConnected returns true if TCP connection is active
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// send sends a command and returns the response line
func (c *Client) send(cmd string) (string, error) {
	if c.conn == nil {
		if err := c.Connect(); err != nil {
			return "", err
		}
	}
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err := fmt.Fprintf(c.conn, "%s\n", cmd)
	if err != nil {
		c.conn = nil
		return "", err
	}
	if c.scanner.Scan() {
		return c.scanner.Text(), nil
	}
	c.conn = nil
	return "", fmt.Errorf("LMS connection closed")
}

// encodeURL encodes a URL for LMS CLI — only spaces need encoding, the rest passes as-is
func encodeURL(rawURL string) string {
	return strings.ReplaceAll(rawURL, " ", "%20")
}

// --- Player commands (MAC = LARA device MAC address) ---

// Play starts playback on the player
func (c *Client) Play(mac string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.send(fmt.Sprintf("%s play", mac))
	return err
}

// Pause toggles pause
func (c *Client) Pause(mac string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.send(fmt.Sprintf("%s pause", mac))
	return err
}

// Stop stops playback
func (c *Client) Stop(mac string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.send(fmt.Sprintf("%s stop", mac))
	return err
}

// SetVolume sets volume 0–100
func (c *Client) SetVolume(mac string, vol int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.send(fmt.Sprintf("%s mixer volume %d", mac, vol))
	return err
}

// PlayURL loads a URL into playlist and starts playback
func (c *Client) PlayURL(mac, streamURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.send(fmt.Sprintf("%s playlist play %s", mac, encodeURL(streamURL))); err != nil {
		return err
	}
	_, err := c.send(fmt.Sprintf("%s play", mac))
	return err
}

// GetStatus returns current player status using individual CLI queries for reliable parsing.
// Each field is queried separately so a missing/empty field doesn't break the rest.
func (c *Client) GetStatus(mac string) (PlayerInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	info := PlayerInfo{MAC: mac, Connected: true}

	// mode: "play" | "stop" | "pause"
	if r, err := c.send(fmt.Sprintf("%s mode ?", mac)); err == nil {
		if p := strings.Fields(r); len(p) >= 3 {
			info.Mode = p[2]
		}
	} else {
		return info, err // connection dead — bail early
	}

	// volume: 0–100
	if r, err := c.send(fmt.Sprintf("%s mixer volume ?", mac)); err == nil {
		if p := strings.Fields(r); len(p) >= 4 {
			fmt.Sscanf(p[3], "%d", &info.Volume)
		}
	}

	// current_title: ICY metadata for live streams (e.g. "Artist - Song")
	if r, err := c.send(fmt.Sprintf("%s current_title ?", mac)); err == nil {
		if p := strings.Fields(r); len(p) >= 3 {
			info.CurrentTitle = urlDecode(strings.Join(p[2:], " "))
		}
	}

	// title: track title (local music / YT Music)
	if r, err := c.send(fmt.Sprintf("%s title ?", mac)); err == nil {
		if p := strings.Fields(r); len(p) >= 3 {
			info.Title = urlDecode(strings.Join(p[2:], " "))
		}
	}

	// artist
	if r, err := c.send(fmt.Sprintf("%s artist ?", mac)); err == nil {
		if p := strings.Fields(r); len(p) >= 3 {
			info.Artist = urlDecode(strings.Join(p[2:], " "))
		}
	}

	// elapsed playback position in seconds
	if r, err := c.send(fmt.Sprintf("%s time ?", mac)); err == nil {
		if p := strings.Fields(r); len(p) >= 3 {
			fmt.Sscanf(p[2], "%f", &info.Elapsed)
		}
	}

	// total duration in seconds (0 for live streams)
	if r, err := c.send(fmt.Sprintf("%s duration ?", mac)); err == nil {
		if p := strings.Fields(r); len(p) >= 3 {
			fmt.Sscanf(p[2], "%f", &info.Duration)
		}
	}

	// current stream URL — "playlist path ?" (LMS echoes ? so value is at p[4])
	if r, err := c.send(fmt.Sprintf("%s playlist path ?", mac)); err == nil {
		p := strings.Fields(r)
		// Response: <mac> playlist path %3F <url>  (%3F = URL-encoded ?)
		if len(p) >= 5 {
			info.StreamURL = urlDecode(strings.Join(p[4:], ""))
		}
	}

	// station name from LMS — "playlist name ?" returns the stream/station title
	// (e.g. "Rádio BLANÍK 87.8", "Evropa 2 88.2")
	if r, err := c.send(fmt.Sprintf("%s playlist name ?", mac)); err == nil {
		if p := strings.Fields(r); len(p) >= 4 {
			t := urlDecode(strings.Join(p[3:], " "))
			if !strings.HasPrefix(t, "http") {
				info.StreamTitle = t
			}
		}
	}

	return info, nil
}

// Skip advances to the next track in the LMS playlist
func (c *Client) Skip(mac string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.send(fmt.Sprintf("%s playlist index +1", mac))
	return err
}

// Prev returns to the previous track in the LMS playlist
func (c *Client) Prev(mac string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.send(fmt.Sprintf("%s playlist index -1", mac))
	return err
}

// ToggleMute toggles mute state on the LMS player
func (c *Client) ToggleMute(mac string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.send(fmt.Sprintf("%s mixer muting toggle", mac))
	return err
}

// GetPlayers returns list of all connected players
func (c *Client) GetPlayers() ([]PlayerInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := c.send("players 0")
	if err != nil {
		return nil, err
	}
	return parsePlayers(resp), nil
}

// GetVolume returns current volume for a player
func (c *Client) GetVolume(mac string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := c.send(fmt.Sprintf("%s mixer volume ?", mac))
	if err != nil {
		return 0, err
	}
	// Response: "MAC mixer volume 50"
	parts := strings.Fields(resp)
	if len(parts) >= 4 {
		var vol int
		fmt.Sscanf(parts[3], "%d", &vol)
		return vol, nil
	}
	return 0, nil
}

// --- Parsing helpers ---


func parsePlayers(resp string) []PlayerInfo {
	// LMS encodes spaces in values as %20, colons as %3A
	decoded, _ := url.QueryUnescape(strings.ReplaceAll(resp, "%3A", ":"))
	_ = decoded

	var players []PlayerInfo
	// Simple: find playerid: entries
	pairs := parseLMSPairs(resp)
	mac := pairs["playerid"]
	if mac == "" {
		return players
	}
	// Decode MAC
	mac, _ = url.QueryUnescape(mac)
	p := PlayerInfo{
		MAC:       mac,
		Name:      urlDecode(pairs["name"]),
		IP:        urlDecode(pairs["ip"]),
		Model:     urlDecode(pairs["model"]),
		Connected: pairs["connected"] == "1",
	}
	fmt.Sscanf(pairs["power"], "%d", &p.Power)
	players = append(players, p)
	return players
}

// parseLMSPairs parses LMS CLI response into key/value map.
// LMS encodes as "key%3Avalue" (key:value URL-encoded) or "key: value" (space-separated).
func parseLMSPairs(s string) map[string]string {
	result := make(map[string]string)
	// Split on spaces — each token is either "key:value" or "key:" with next token as value
	parts := strings.Fields(s)
	for i := 0; i < len(parts); i++ {
		// URL decode each token individually
		token, _ := url.QueryUnescape(parts[i])
		if idx := strings.Index(token, ":"); idx > 0 {
			key := token[:idx]
			val := token[idx+1:]
			if val == "" && i+1 < len(parts) {
				// "key:" — take next token as value only if it's not itself a key
				next, _ := url.QueryUnescape(parts[i+1])
				if !strings.Contains(next, ":") {
					val = next
					i++
				}
			}
			result[key] = val
		}
	}
	return result
}

func urlDecode(s string) string {
	d, _ := url.QueryUnescape(s)
	return d
}

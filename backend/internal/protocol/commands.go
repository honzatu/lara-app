// Copyright (c) 2026 honzatu. MIT License.
// LARA binary protocol — HTTP/1.0 POST /data command layer
package protocol

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// Command bytes
	CmdPlay    byte = 0x03
	CmdStop    byte = 0x04
	CmdVolume  byte = 0x05
	CmdVolUp   byte = 0x06
	CmdVolDown byte = 0x07
	CmdNext    byte = 0x0A
	CmdPrev    byte = 0x0B

	// Config sub-commands
	SubLoadPage0 byte = 0x06
	SubLoadPage1 byte = 0x0C
	SubLoadPage2 byte = 0x0D
	SubLoadPage3 byte = 0x0E
	SubSavePage  byte = 0x08

	defaultUser  = "admin"
	defaultRealm = "LARA"
	// Static nonce — same across all known LARA firmware versions
	staticNonce = "dcd98b7102dd2f0e8b11d0f600bfb0c093"
)

// Status holds the parsed 6-byte response from LARA
type Status struct {
	StationIndex int
	Volume       int
	Playing      bool
	Raw          []byte
}

// Client communicates with a single LARA device via raw TCP HTTP/1.0
// LARA firmware returns 404 on HTTP/1.1 — raw sockets are the reliable approach
type Client struct {
	IP       string
	Password string
}

// NewClient creates a LARA protocol client for the given IP
func NewClient(ip, password string) *Client {
	return &Client{IP: ip, Password: password}
}

// post sends a binary payload to POST /data via raw TCP HTTP/1.0
func (c *Client) post(payload []byte) ([]byte, error) {
	conn, err := net.DialTimeout("tcp", c.IP+":80", 4*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(8 * time.Second))

	auth := c.digestAuth("POST", "/data")
	req := fmt.Sprintf(
		"POST /data HTTP/1.0\r\nHost: %s\r\nContent-Type: application/octet-stream\r\nAuthorization: %s\r\nContent-Length: %d\r\n\r\n",
		c.IP, auth, len(payload),
	)
	if _, err := conn.Write(append([]byte(req), payload...)); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read HTTP response
	reader := bufio.NewReader(conn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read status: %w", err)
	}
	parts := strings.Fields(statusLine)
	if len(parts) < 2 || parts[1] != "200" {
		code := "?"
		if len(parts) >= 2 { code = parts[1] }
		return nil, fmt.Errorf("HTTP %s", code)
	}

	// Read headers to find Content-Length
	bodyLen := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			bodyLen, _ = strconv.Atoi(strings.TrimSpace(line[15:]))
		}
	}

	// Read body
	if bodyLen > 0 {
		body := make([]byte, bodyLen)
		var buf bytes.Buffer
		for buf.Len() < bodyLen {
			tmp := make([]byte, bodyLen-buf.Len())
			n, err := reader.Read(tmp)
			buf.Write(tmp[:n])
			if err != nil { break }
		}
		copy(body, buf.Bytes())
		return body, nil
	}
	return nil, nil
}

// digestAuth computes Digest Authorization header using LARA's static nonce
func (c *Client) digestAuth(method, path string) string {
	ha1 := md5hex(defaultUser + ":" + defaultRealm + ":" + c.Password)
	ha2 := md5hex(method + ":" + path)
	response := md5hex(ha1 + ":" + staticNonce + ":" + ha2)
	return fmt.Sprintf(
		`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
		defaultUser, defaultRealm, staticNonce, path, response,
	)
}

func md5hex(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

// parseStatus parses the 6-byte status response from LARA
func parseStatus(b []byte) Status {
	if len(b) < 6 {
		return Status{Raw: b}
	}
	return Status{
		StationIndex: int(b[2]),
		Volume:       int(b[3]),
		Playing:      b[5] == 1,
		Raw:          b,
	}
}

// --- Public command methods ---

// GetStatus queries current LARA state (play/stop, volume, station)
func (c *Client) GetStatus() (Status, error) {
	resp, err := c.post([]byte{0xFF, 0xFB, 0xFB, 0xCC, CmdPlay, 0x00})
	if err != nil {
		return Status{}, err
	}
	return parseStatus(resp), nil
}

// Play starts playback (same packet as GetStatus — LARA plays if stopped)
func (c *Client) Play() (Status, error) {
	return c.GetStatus()
}

// Stop stops playback
func (c *Client) Stop() error {
	_, err := c.post([]byte{0xFF, 0xFB, 0xFB, 0xCC, CmdStop, 0x00})
	return err
}

// SetVolume sets volume 0–100
func (c *Client) SetVolume(vol int) error {
	if vol < 0 { vol = 0 }
	if vol > 100 { vol = 100 }
	_, err := c.post([]byte{0xFF, 0xFB, 0xFB, 0xCC, CmdVolume, byte(vol)})
	return err
}

// VolumeUp increases volume by 5
func (c *Client) VolumeUp() (Status, error) {
	resp, err := c.post([]byte{0xFF, 0xFB, 0xFB, 0xCC, CmdVolUp, 0x00})
	if err != nil {
		return Status{}, err
	}
	return parseStatus(resp), nil
}

// VolumeDown decreases volume by 5
func (c *Client) VolumeDown() (Status, error) {
	resp, err := c.post([]byte{0xFF, 0xFB, 0xFB, 0xCC, CmdVolDown, 0x00})
	if err != nil {
		return Status{}, err
	}
	return parseStatus(resp), nil
}

// Next skips to next station
func (c *Client) Next() error {
	_, err := c.post([]byte{0xFF, 0xFB, 0xFB, 0xCC, CmdNext, 0x00})
	return err
}

// Prev goes to previous station
func (c *Client) Prev() error {
	_, err := c.post([]byte{0xFF, 0xFB, 0xFB, 0xCC, CmdPrev, 0x00})
	return err
}

// IsOnline returns true if LARA responds to HTTP
func (c *Client) IsOnline() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:80", c.IP), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

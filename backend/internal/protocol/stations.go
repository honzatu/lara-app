// Copyright (c) 2026 honzatu. MIT License.
// LARA station page read/write (40 slots across 4 pages)
package protocol

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net/url"
	"strings"
	"time"
)

const (
	StationRecordSize = 139
	StationsPerPage   = 10
	TotalPages        = 4
	TotalStations     = TotalPages * StationsPerPage // 40

	NameSize   = 13
	DomainSize = 50
	FileSize   = 70
	IPSize     = 4
	PortSize   = 2
)

// Station represents a single LARA radio station slot
type Station struct {
	Index  int
	Name   string
	Domain string
	File   string // URL path after host
	IP     [4]byte
	Port   uint16
}

// URL reconstructs the full stream URL from station fields
func (s Station) URL() string {
	if s.Domain != "" {
		return fmt.Sprintf("http://%s/%s", s.Domain, s.File)
	}
	return fmt.Sprintf("http://%d.%d.%d.%d:%d/%s",
		s.IP[0], s.IP[1], s.IP[2], s.IP[3], s.Port, s.File)
}

// StationFromURL parses a stream URL into a Station record
func StationFromURL(name, rawURL string) (Station, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Station{}, err
	}
	s := Station{Name: name}
	host := u.Hostname()
	port := uint16(80)
	if p := u.Port(); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	s.Port = port
	s.File = strings.TrimPrefix(u.Path, "/")
	if u.RawQuery != "" {
		s.File += "?" + u.RawQuery
	}

	// Try to parse host as IP
	var ip [4]byte
	n, _ := fmt.Sscanf(host, "%d.%d.%d.%d", &ip[0], &ip[1], &ip[2], &ip[3])
	if n == 4 {
		s.IP = ip
	} else {
		s.Domain = host
	}
	return s, nil
}

// pageSubCommand returns the config sub-command for the given page (0–3)
func pageSubCommand(page int) byte {
	return []byte{SubLoadPage0, SubLoadPage1, SubLoadPage2, SubLoadPage3}[page]
}

// LoadStationPage reads 10 stations from LARA flash (page 0–3)
func (c *Client) LoadStationPage(page int) ([]Station, int, error) {
	if page < 0 || page >= TotalPages {
		return nil, 0, fmt.Errorf("invalid page %d", page)
	}

	r1 := byte(rand.Intn(124) + 1)
	r2 := byte(rand.Intn(124) + 126)
	pkt := []byte{0xFF, 0xFA, 0xFA, 0xFF, r1, r2, 0x00, 0x80, 0xC0, pageSubCommand(page)}

	resp, err := c.post(pkt)
	if err != nil {
		return nil, 0, err
	}
	if len(resp) < 1404 {
		return nil, 0, fmt.Errorf("short response %d bytes", len(resp))
	}

	totalCount := int(resp[12])
	stations := make([]Station, 0, StationsPerPage)

	for i := 0; i < StationsPerPage; i++ {
		offset := 13 + i*StationRecordSize
		if offset+StationRecordSize > len(resp) {
			break
		}
		rec := resp[offset : offset+StationRecordSize]
		s := Station{
			Index:  page*StationsPerPage + i,
			Name:   CP1250ToUTF8(rec[0:NameSize]),
			Domain: strings.TrimRight(string(rec[13:13+DomainSize]), "\x00"),
			File:   strings.TrimRight(string(rec[63:63+FileSize]), "\x00"),
			Port:   binary.BigEndian.Uint16(rec[137:139]),
		}
		copy(s.IP[:], rec[133:137])
		stations = append(stations, s)
	}
	return stations, totalCount, nil
}

// SaveStationPage writes 10 stations to LARA flash (page 0–3)
// All 10 slots are written — this is required for next/prev button to work correctly
func (c *Client) SaveStationPage(page, totalCount int, stations []Station) error {
	if page < 0 || page >= TotalPages {
		return fmt.Errorf("invalid page %d", page)
	}

	r1 := byte(rand.Intn(124) + 1)
	r2 := byte(rand.Intn(124) + 126)

	// Build 1440-byte payload (10 bytes header + 1430 bytes data)
	payload := make([]byte, 1450)
	payload[0] = 0xFF; payload[1] = 0xFA; payload[2] = 0xFA; payload[3] = 0xFF
	payload[4] = r1; payload[5] = r2
	payload[6] = 0x00; payload[7] = 0x80; payload[8] = 0xC0; payload[9] = SubSavePage
	payload[45] = byte(page)
	payload[46] = byte(totalCount)

	for i := 0; i < StationsPerPage; i++ {
		offset := 47 + i*StationRecordSize
		if i >= len(stations) {
			break
		}
		s := stations[i]
		rec := payload[offset : offset+StationRecordSize]

		name := UTF8ToCP1250(s.Name, NameSize)
		copy(rec[0:NameSize], name)
		copy(rec[13:13+DomainSize], []byte(s.Domain))
		copy(rec[63:63+FileSize], []byte(s.File))
		copy(rec[133:137], s.IP[:])
		binary.BigEndian.PutUint16(rec[137:139], s.Port)
	}

	_, err := c.post(payload)
	return err
}

// LaraPlayStream implements the full stream playback sequence:
// 1. Query current station index
// 2. Load that page
// 3. Write URL to slot 0 only — slots 1+ keep synced favorites,
//    so the physical next/prev buttons still walk the favorites list
// 4. Save page
// 5. Wait for flash write
// 6. Send PLAY
func (c *Client) LaraPlayStream(streamURL, stationName string) error {
	// 1. Get current station
	status, err := c.GetStatus()
	if err != nil {
		return fmt.Errorf("status query: %w", err)
	}
	page := status.StationIndex / StationsPerPage

	// 2. Load current page
	stations, totalCount, err := c.LoadStationPage(page)
	if err != nil {
		return fmt.Errorf("load page: %w", err)
	}

	// 3. Build station from URL, write to slot 0 of the current page
	station, err := StationFromURL(stationName, streamURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	stations[0] = station
	if totalCount < 1 {
		totalCount = 1
	}

	// 4. Save page
	if err := c.SaveStationPage(page, totalCount, stations); err != nil {
		return fmt.Errorf("save page: %w", err)
	}

	// 5. Wait for LARA flash write
	time.Sleep(600 * time.Millisecond)

	// 6. Play
	_, err = c.Play()
	return err
}

// PlayRadioAt plays a stream while preserving the synced favorites in slots 1+.
// Slot 0 (current playing) is always overwritten; if position points at the
// favorite's own slot (1–19), that slot is refreshed too so the list stays
// consistent. position < 0 means "not a favorite" (new station, YouTube, …).
func (c *Client) PlayRadioAt(streamURL, stationName string, position int) error {
	stations, totalCount, err := c.LoadStationPage(0)
	if err != nil {
		return fmt.Errorf("load page 0: %w", err)
	}

	station, err := StationFromURL(stationName, streamURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}

	stations[0] = station
	if position >= 1 && position <= 9 {
		stations[position] = station
	}
	if totalCount < 1 {
		totalCount = 1
	}
	if err := c.SaveStationPage(0, totalCount, stations); err != nil {
		return fmt.Errorf("save page 0: %w", err)
	}

	if position >= 10 && position <= 19 {
		if page1, cnt1, err := c.LoadStationPage(1); err == nil {
			page1[position-10] = station
			_ = c.SaveStationPage(1, cnt1, page1)
		}
	}

	time.Sleep(600 * time.Millisecond)
	_, err = c.Play()
	return err
}

// NamedStream is a name+URL pair used when syncing favorites into LARA slots.
type NamedStream struct {
	Name string
	URL  string
}

// SyncStations writes favorite streams into the physical LARA station slots.
// Slot 0 is left empty (reserved for "current playing" written by playback),
// slots 1–39 hold the favorites. All 4 pages are always written: LARA reads
// stations_count from every page and uses the last value, so skipping pages
// would leave a stale count and hide stations in the device menu.
// Writes pause 800 ms between pages to let LARA finish each flash write.
func (c *Client) SyncStations(streams []NamedStream) (int, error) {
	maxStreams := len(streams)
	if maxStreams > TotalStations-1 {
		maxStreams = TotalStations - 1
	}
	totalCount := 1 + maxStreams

	var firstErr error
	for page := 0; page < TotalPages; page++ {
		stations := make([]Station, StationsPerPage)
		for slot := 0; slot < StationsPerPage; slot++ {
			globalSlot := page*StationsPerPage + slot
			if globalSlot == 0 || globalSlot-1 >= maxStreams {
				continue // slot 0 reserved, rest stays empty
			}
			s, err := StationFromURL(streams[globalSlot-1].Name, streams[globalSlot-1].URL)
			if err != nil {
				continue
			}
			stations[slot] = s
		}
		if err := c.SaveStationPage(page, totalCount, stations); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("save page %d: %w", page, err)
		}
		time.Sleep(800 * time.Millisecond)
	}
	return maxStreams, firstErr
}

#!/usr/bin/env python3
"""
music-bridge — YTMusic ↔ LARA audio bridge
v3: seek, prev, infinite queue, volume persistence via Go backend
"""

import glob
import json
import os
import re
import subprocess
import threading
import time
import uuid
import socket
import ssl
import unicodedata
import urllib.request
from urllib.parse import urlparse
from subprocess import PIPE
from flask import Flask, request, Response, jsonify
from ytmusicapi import YTMusic
import ytmusicapi

app = Flask(__name__)

# ── YTMusic initialization ────────────────────────────────────────────────────
headers_txt = "headers.txt"
browser_json = "browser.json"

if os.path.exists(headers_txt):
    try:
        with open(headers_txt, "r", encoding="utf-8") as f:
            raw_headers = f.read()
        ytmusicapi.setup(filepath=browser_json, headers_raw=raw_headers)
        print("[Bridge] browser.json generated from headers.txt.")
    except Exception as e:
        print(f"[Bridge] Error converting headers.txt: {e}")

if os.path.exists(browser_json):
    try:
        yt_music = YTMusic(browser_json)
        print("[Bridge] Initialized with Premium account.")
    except Exception as e:
        print(f"[Bridge] Fallback to anonymous: {e}")
        yt_music = YTMusic()
else:
    yt_music = YTMusic()


# ── Warmup cache (pre-downloaded files) ──────────────────────────────────────

# Maps video_id → absolute path of pre-downloaded audio file.
# Populated by /api/warmup; consumed (popped) by download_audio().
_warmup_cache: dict[str, str] = {}
_warmup_cache_lock = threading.Lock()


# ── Audio helpers ─────────────────────────────────────────────────────────────

def yt_url(video_id: str) -> str:
    return f"https://music.youtube.com/watch?v={video_id}"


def download_audio(video_id: str) -> str:
    """
    Return path to the audio file for video_id (downloads if not already cached).

    Warmup pre-downloads the file so this returns immediately when LARA connects.
    Without the cache the generator would block for 10–90 s before yielding the
    first byte, causing LARA to time out and produce no sound.

    Why temp file instead of a direct yt-dlp → ffmpeg pipe:
    1. DASH fragments: yt-dlp downloads YouTube audio in 5–15 s fragments.
       Between fragments it pauses, starving ffmpeg's pipe → LARA pauses every ~15 s.
    2. Timestamp resets: each DASH fragment resets its PTS to 0. ffmpeg's -re flag
       interprets this as "I'm 15 s behind real-time" and rushes → 2× speed on LARA.
    3. Seek: ffmpeg can only seek (-ss) inside a seekable file, not a live pipe.

    A complete, pre-downloaded file has monotonic timestamps and no gaps, so -re
    works correctly, the stream is smooth, and seeks are possible.
    """
    # ── Check warmup cache first ──────────────────────────────────────────────
    with _warmup_cache_lock:
        cached_path = _warmup_cache.pop(video_id, None)
    if cached_path and os.path.exists(cached_path):
        size = os.path.getsize(cached_path)
        if size >= 4096:
            print(f"[Bridge] Using warmup cache for {video_id}: {cached_path} ({size // 1024} KB)")
            return cached_path
        # Too small — discard and re-download
        try:
            os.unlink(cached_path)
        except Exception:
            pass

    unique = uuid.uuid4().hex
    out_prefix = f"/tmp/bridge_{unique}"
    out_template = f"{out_prefix}.%(ext)s"

    print(f"[Bridge] Downloading {video_id} → {out_prefix}.*")
    dl = subprocess.Popen(
        ["yt-dlp", "--quiet", "-f", "bestaudio", "--no-part",
         "-o", out_template, yt_url(video_id)],
        stderr=subprocess.DEVNULL,
    )
    dl.wait()

    candidates = glob.glob(f"{out_prefix}.*")
    if not candidates:
        raise RuntimeError(f"yt-dlp produced no file for {video_id}")
    tmp_path = candidates[0]

    file_size = os.path.getsize(tmp_path)
    if file_size < 4096:
        try:
            os.unlink(tmp_path)
        except Exception:
            pass
        raise RuntimeError(f"Downloaded file too small ({file_size} B) for {video_id}")

    print(f"[Bridge] Downloaded {file_size // 1024} KB → {tmp_path}")
    return tmp_path


def get_file_duration(path: str) -> float:
    """Use ffprobe to get audio duration in seconds. Returns 0.0 on failure."""
    try:
        result = subprocess.run(
            ["ffprobe", "-v", "quiet", "-show_entries", "format=duration",
             "-of", "default=noprint_wrappers=1:nokey=1", path],
            capture_output=True, text=True, timeout=10,
        )
        return float(result.stdout.strip())
    except Exception:
        return 0.0


def spawn_ffmpeg(input_path: str, seek_seconds: float = 0.0) -> subprocess.Popen:
    """
    Start ffmpeg reading from a file (not a pipe), encoding to MP3 on stdout.

    Using the actual file path instead of pipe:0 lets ffmpeg:
    - Read at native rate (-re) correctly (complete file → monotonic timestamps)
    - Seek with -ss before opening the file (accurate, fast)
    - Avoid chunked-encoding issues (Content-Length header disables those)
    """
    cmd = ["ffmpeg", "-loglevel", "error", "-re"]
    if seek_seconds > 0:
        # Place -ss before -i so ffmpeg seeks during demux (fast, accurate for audio)
        cmd += ["-ss", f"{seek_seconds:.3f}"]
    cmd += [
        "-i", input_path,
        "-acodec", "libmp3lame", "-ab", "128k", "-ac", "2", "-ar", "44100",
        "-write_xing", "0",       # no Xing/INFO header
        "-id3v2_version", "0",   # no ID3v2 tag — embedded players may mis-parse it as audio
        "-write_id3v1", "0",     # no ID3v1 tag
        "-f", "mp3", "pipe:1",
    ]
    return subprocess.Popen(cmd, stdout=PIPE, stderr=subprocess.DEVNULL)


def kill_procs(*procs):
    """Kill processes silently, then wait to reap zombies."""
    for p in procs:
        if p is not None:
            try:
                p.kill()
            except Exception:
                pass
            try:
                p.wait(timeout=2)
            except Exception:
                pass


# ── SessionQueue ──────────────────────────────────────────────────────────────

class SessionQueue:
    def __init__(self, session_id: str):
        self.id = session_id
        self.tracks: list[dict] = []       # [{videoId, title, artist, ...}]
        self.current_index: int = 0

        # Control flags (checked per chunk — GIL makes bool reads atomic)
        self.skip_requested: bool = False
        self.stop_requested: bool = False
        self.seek_requested: bool = False
        self.seek_seconds: float = 0.0
        self.prev_requested: bool = False

        self.lock = threading.Lock()
        self.created_at: float = time.time()
        self.last_active: float = time.time()

        # Current track state (updated by generator)
        self._ffmpeg: subprocess.Popen | None = None
        self._track_start: float = 0.0      # wall time when current track started (adjusted on seek)
        self._track_duration: float = 0.0   # total duration of current track in seconds
        self._tmp_path: str | None = None   # temp file path for current track

        # Queue extension flag (prevents concurrent extend calls)
        self._extending_queue: bool = False


_sessions: dict[str, SessionQueue] = {}
_sessions_lock = threading.Lock()


def get_session(session_id: str) -> SessionQueue | None:
    with _sessions_lock:
        return _sessions.get(session_id)


# ── Queue extension (infinite autoplay) ──────────────────────────────────────

def _get_related_tracks(video_id: str) -> list[dict]:
    """
    Get related tracks for autoplay queue extension.

    Primary: ytmusicapi get_watch_playlist(radio=True) — rich metadata.
    Fallback: yt-dlp flat playlist on RDAMVM radio list — always works,
              because yt-dlp is updated more frequently than ytmusicapi.
    """
    # 1) Try ytmusicapi first
    try:
        result = yt_music.get_watch_playlist(video_id, radio=True)
        tracks = []
        for t in result.get("tracks", []):
            vid = t.get("videoId")
            if not vid:
                continue
            tracks.append({
                "videoId": vid,
                "title":   t.get("title", ""),
                "artist":  ", ".join([a.get("name", "") for a in t.get("artists", [])]),
                "duration": t.get("length"),
                "thumbnail": (t.get("thumbnail") or [{}])[0].get("url", ""),
            })
        if tracks:
            return tracks
    except Exception as e:
        print(f"[Bridge] get_watch_playlist failed ({e}), falling back to yt-dlp")

    # 2) Fallback: yt-dlp flat-playlist on YouTube Music RDAMVM radio URL
    try:
        radio_url = f"https://music.youtube.com/watch?v={video_id}&list=RDAMVM{video_id}"
        proc = subprocess.run(
            ["yt-dlp", "--flat-playlist", "--dump-json", "--no-warnings",
             "--playlist-end", "25", "-q", radio_url],
            capture_output=True, text=True, timeout=30,
        )
        tracks = []
        for line in proc.stdout.splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                entry = json.loads(line)
                vid = entry.get("id") or entry.get("videoId")
                if not vid or vid == video_id:
                    continue
                tracks.append({
                    "videoId":   vid,
                    "title":     entry.get("title", ""),
                    "artist":    entry.get("uploader") or entry.get("channel") or "",
                    "duration":  None,
                    "thumbnail": "",
                })
            except Exception:
                continue
        return tracks
    except Exception as e:
        print(f"[Bridge] yt-dlp radio fallback failed: {e}")
        return []


def _extend_queue(session: SessionQueue, seed_video_id: str):
    """
    Background: fetch more tracks via YT Music radio and append to queue.
    Called when ≤2 tracks remain after current. Prevents silence at end of list.
    """
    sid = session.id[:8]
    print(f"[Bridge][{sid}] Extending queue from seed {seed_video_id}")
    try:
        tracks = _get_related_tracks(seed_video_id)
        if tracks:
            with session.lock:
                existing_ids = {t.get("videoId") for t in session.tracks}
                new_tracks = [t for t in tracks if t["videoId"] not in existing_ids]
                session.tracks.extend(new_tracks)
            print(f"[Bridge][{sid}] Queue extended: +{len(new_tracks)} tracks")
        else:
            print(f"[Bridge][{sid}] Queue extension returned no tracks")
    except Exception as e:
        print(f"[Bridge][{sid}] Queue extension failed: {e}")
    finally:
        session._extending_queue = False


# ── Pre-warm ──────────────────────────────────────────────────────────────────

def _prewarm_next(session: SessionQueue, index: int):
    """Background: verify next track is available (yt-dlp --simulate)."""
    try:
        vid = session.tracks[index]["videoId"]
        subprocess.run(
            ["yt-dlp", "--simulate", "--quiet", yt_url(vid)],
            timeout=30, check=True,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        print(f"[Bridge][{session.id[:8]}] Pre-warm OK track {index}: {vid}")
    except Exception as e:
        print(f"[Bridge][{session.id[:8]}] Pre-warm failed track {index}: {e}")


# ── Session streaming generator ───────────────────────────────────────────────

def _generate_session_stream(session: SessionQueue):
    """
    Flask streaming generator. Yields MP3 chunks from sequential tracks.
    LARA keeps one HTTP connection open; bridge switches songs internally.

    Supports:
    - Skip: kill ffmpeg, advance index, start next track
    - Prev: kill ffmpeg, decrement index, restart from that track
    - Seek: kill ffmpeg, restart with -ss <seconds> (same file, no re-download)
    - Infinite queue: auto-extend when ≤2 tracks remain
    """
    sid = session.id[:8]

    while True:   # ── outer: track loop ────────────────────────────────────
        with session.lock:
            if session.stop_requested:
                print(f"[Bridge][{sid}] Stop before track.")
                break
            idx = session.current_index
            if idx >= len(session.tracks):
                print(f"[Bridge][{sid}] Queue exhausted.")
                break
            track = session.tracks[idx]

        vid = track.get("videoId") or track.get("id", "")
        title = track.get("title", vid)
        print(f"[Bridge][{sid}] Track {idx}: {title} ({vid})")

        # Pre-warm next track in background
        with session.lock:
            next_idx = idx + 1
            has_next = next_idx < len(session.tracks)
        if has_next:
            threading.Thread(
                target=_prewarm_next, args=(session, next_idx), daemon=True
            ).start()

        # Auto-extend queue when ≤2 tracks remain
        with session.lock:
            remaining = len(session.tracks) - (idx + 1)
            extending = session._extending_queue
        if remaining <= 2 and not extending:
            session._extending_queue = True
            threading.Thread(
                target=_extend_queue, args=(session, vid), daemon=True
            ).start()

        # ── Download ──────────────────────────────────────────────────────
        try:
            tmp_path = download_audio(vid)
        except Exception as e:
            print(f"[Bridge][{sid}] Download failed: {e}")
            with session.lock:
                session.current_index += 1
            continue

        duration = get_file_duration(tmp_path)
        print(f"[Bridge][{sid}] Duration: {duration:.1f}s")

        with session.lock:
            session._tmp_path = tmp_path
            session._track_duration = duration
            session._track_start = time.time()
            session.last_active = time.time()

        seek_offset = 0.0   # seconds; updated on seek

        try:
            while True:   # ── middle: ffmpeg restart loop (seek causes restart) ─
                ffmpeg = spawn_ffmpeg(tmp_path, seek_offset)
                with session.lock:
                    session._ffmpeg = ffmpeg

                restart_ffmpeg = False   # set True only on seek

                try:
                    while True:   # ── inner: chunk read loop ──────────────────
                        if session.stop_requested:
                            print(f"[Bridge][{sid}] Stop mid-track {idx}.")
                            kill_procs(ffmpeg)
                            return   # ends generator → ends HTTP response

                        if session.skip_requested:
                            print(f"[Bridge][{sid}] Skip track {idx}.")
                            kill_procs(ffmpeg)
                            with session.lock:
                                session.skip_requested = False
                                session.current_index += 1
                            break   # → break ffmpeg loop → cleanup → next track

                        if session.prev_requested:
                            print(f"[Bridge][{sid}] Prev from track {idx}.")
                            kill_procs(ffmpeg)
                            with session.lock:
                                session.prev_requested = False
                                if session.current_index > 0:
                                    session.current_index -= 1
                            break   # → break ffmpeg loop → cleanup → that track

                        if session.seek_requested:
                            seek_secs = session.seek_seconds
                            print(f"[Bridge][{sid}] Seek to {seek_secs:.1f}s")
                            kill_procs(ffmpeg)
                            with session.lock:
                                session.seek_requested = False
                                # Adjust _track_start so elapsed stays consistent
                                session._track_start = time.time() - seek_secs
                            seek_offset = seek_secs
                            restart_ffmpeg = True
                            break   # → continue ffmpeg loop with new seek

                        chunk = ffmpeg.stdout.read(4096)
                        if not chunk:
                            # Track ended naturally
                            print(f"[Bridge][{sid}] Track {idx} ended naturally.")
                            kill_procs(ffmpeg)
                            with session.lock:
                                session.current_index += 1
                            break   # → break ffmpeg loop → cleanup → next track

                        session.last_active = time.time()
                        yield chunk

                except GeneratorExit:
                    kill_procs(ffmpeg)
                    raise   # propagate to outer try for file cleanup
                except Exception as e:
                    print(f"[Bridge][{sid}] Stream error track {idx}: {e}")
                    kill_procs(ffmpeg)
                    with session.lock:
                        session.current_index += 1

                if restart_ffmpeg:
                    continue   # restart same track with new seek offset
                else:
                    break      # exit ffmpeg restart loop → next track

        except GeneratorExit:
            print(f"[Bridge][{sid}] Client disconnected at track {idx}.")
            try:
                os.unlink(tmp_path)
            except Exception:
                pass
            return

        # Cleanup temp file for finished/skipped/prev track
        try:
            os.unlink(tmp_path)
        except Exception:
            pass
        with session.lock:
            session._tmp_path = None

    print(f"[Bridge][{sid}] Session stream ended.")


# ── Session cleanup thread ────────────────────────────────────────────────────

SESSION_IDLE_TTL = 1800         # 30 min of inactivity → clean up
SESSION_CLEANUP_INTERVAL = 300  # check every 5 min


def _cleanup_loop():
    while True:
        time.sleep(SESSION_CLEANUP_INTERVAL)
        now = time.time()
        stale = []
        with _sessions_lock:
            for sid, sess in _sessions.items():
                if (now - sess.last_active) > SESSION_IDLE_TTL:
                    stale.append(sid)
            for sid in stale:
                sess = _sessions.pop(sid)
                sess.stop_requested = True
                kill_procs(sess._ffmpeg)
                print(f"[Bridge] Cleaned up idle session {sid[:8]}")


threading.Thread(target=_cleanup_loop, daemon=True).start()


# ── Session endpoints ─────────────────────────────────────────────────────────

STREAM_HEADERS = {
    "Cache-Control": "no-cache, no-store",
    "Connection": "keep-alive",
    # Large fake Content-Length prevents HTTP/1.1 chunked transfer-encoding.
    # Chunked encoding corrupts MP3 streams for many embedded radio players
    # (chunk-size hex headers appear as garbage bytes in the audio).
    "Content-Length": "9999999999",
    # ICY headers — tell LARA the bitrate so it uses correct buffer sizing
    "icy-br": "128",
    "icy-name": "LARA Music Bridge",
    "icy-pub": "0",
}


@app.route("/api/session/create", methods=["POST"])
def session_create():
    session_id = str(uuid.uuid4())
    sess = SessionQueue(session_id)
    with _sessions_lock:
        _sessions[session_id] = sess
    print(f"[Bridge] Created session {session_id[:8]}")
    return jsonify({"sessionId": session_id})


@app.route("/api/session/<session_id>/add", methods=["POST"])
def session_add(session_id: str):
    sess = get_session(session_id)
    if not sess:
        return jsonify({"error": "session not found"}), 404
    tracks = request.get_json(force=True)
    if not isinstance(tracks, list):
        return jsonify({"error": "expected list of tracks"}), 400
    with sess.lock:
        sess.tracks.extend(tracks)
        count = len(sess.tracks)
    print(f"[Bridge][{session_id[:8]}] Added {len(tracks)} tracks (total {count})")
    return jsonify({"sessionId": session_id, "trackCount": count})


@app.route("/stream/session", methods=["GET"])
def stream_session():
    session_id = request.args.get("id")
    if not session_id:
        return "Missing id", 400
    sess = get_session(session_id)
    if not sess:
        return "Session not found", 404
    print(f"[Bridge] LARA connected to session {session_id[:8]}")
    return Response(
        _generate_session_stream(sess),
        mimetype="audio/mpeg",
        headers=STREAM_HEADERS,
    )


@app.route("/api/session/<session_id>/skip", methods=["POST"])
def session_skip(session_id: str):
    sess = get_session(session_id)
    if not sess:
        return jsonify({"error": "session not found"}), 404
    sess.skip_requested = True
    print(f"[Bridge][{session_id[:8]}] Skip requested")
    return jsonify({"ok": True})


@app.route("/api/session/<session_id>/prev", methods=["POST"])
def session_prev(session_id: str):
    sess = get_session(session_id)
    if not sess:
        return jsonify({"error": "session not found"}), 404
    sess.prev_requested = True
    print(f"[Bridge][{session_id[:8]}] Prev requested")
    return jsonify({"ok": True})


@app.route("/api/session/<session_id>/seek", methods=["POST"])
def session_seek(session_id: str):
    sess = get_session(session_id)
    if not sess:
        return jsonify({"error": "session not found"}), 404
    body = request.get_json(force=True) or {}
    try:
        seconds = float(body.get("seconds", 0))
    except (TypeError, ValueError):
        return jsonify({"error": "invalid seconds"}), 400
    seconds = max(0.0, seconds)
    sess.seek_seconds = seconds
    sess.seek_requested = True
    print(f"[Bridge][{session_id[:8]}] Seek requested to {seconds:.1f}s")
    return jsonify({"ok": True, "seconds": seconds})


@app.route("/api/session/<session_id>/stop", methods=["POST"])
def session_stop(session_id: str):
    sess = get_session(session_id)
    if not sess:
        return jsonify({"error": "session not found"}), 404
    sess.stop_requested = True
    kill_procs(sess._ffmpeg)
    print(f"[Bridge][{session_id[:8]}] Stop requested")
    return jsonify({"ok": True})


@app.route("/api/session/<session_id>/status", methods=["GET"])
def session_status(session_id: str):
    sess = get_session(session_id)
    if not sess:
        return jsonify({"error": "session not found"}), 404
    with sess.lock:
        idx = sess.current_index
        tracks = list(sess.tracks)
        elapsed = time.time() - sess._track_start if sess._track_start else 0.0
        duration = sess._track_duration
    track = tracks[idx] if idx < len(tracks) else None
    return jsonify({
        "sessionId": session_id,
        "currentIndex": idx,
        "queueLength": len(tracks),
        "track": track,
        "elapsed": round(elapsed, 1),
        "duration": round(duration, 1),
    })


# ── /api/warmup — verify video is available (backward compat) ────────────────

@app.route("/api/warmup", methods=["GET"])
def warmup():
    """
    Pre-download the audio file so the session stream generator can serve it
    immediately when LARA connects — eliminating LARA's initial-buffer timeout.

    Previously used yt-dlp --simulate (fast, ~2 s) but that left the actual
    download to happen when LARA connected, causing a 10–90 s stall before the
    first byte → LARA timed out → no sound.  Now we download here (blocking),
    cache the path, and the generator picks it up from the cache.
    """
    video_id = request.args.get("video_id") or request.args.get("videoId")
    if not video_id:
        return jsonify({"error": "missing video_id"}), 400
    try:
        # Evict any stale cached file for this video_id
        with _warmup_cache_lock:
            old = _warmup_cache.pop(video_id, None)
        if old:
            try:
                os.unlink(old)
            except Exception:
                pass

        tmp_path = download_audio(video_id)   # blocking — takes 5–30 s
        with _warmup_cache_lock:
            _warmup_cache[video_id] = tmp_path
        print(f"[Bridge] Warmup cached {video_id} → {tmp_path}")
        return jsonify({"ready": True, "video_id": video_id})
    except Exception as e:
        print(f"[Bridge] Warmup FAIL {video_id}: {e}")
        return jsonify({"ready": False, "error": str(e)}), 500


# ── /stream/lara — single-song MP3 stream (backward compat, speed fixed) ─────

@app.route("/stream/lara", methods=["GET"])
def stream_to_lara():
    video_id = request.args.get("video_id") or request.args.get("videoId")
    if not video_id:
        return "Missing video_id", 400

    print(f"[Bridge] /stream/lara {video_id}")
    try:
        tmp_path = download_audio(video_id)
    except Exception as e:
        print(f"[Bridge] /stream/lara download error: {e}")
        return str(e), 500

    ffmpeg = spawn_ffmpeg(tmp_path)

    def generate():
        try:
            while True:
                chunk = ffmpeg.stdout.read(4096)
                if not chunk:
                    break
                yield chunk
        finally:
            print(f"[Bridge] /stream/lara ended {video_id}")
            kill_procs(ffmpeg)
            try:
                os.unlink(tmp_path)
            except Exception:
                pass

    return Response(generate(), mimetype="audio/mpeg", headers=STREAM_HEADERS)


# ── /api/music/search ─────────────────────────────────────────────────────────

@app.route("/api/music/search", methods=["GET"])
def search():
    query = request.args.get("q", "")
    if not query:
        return jsonify([])
    try:
        results = yt_music.search(query, filter="songs")
        songs = []
        for r in results:
            songs.append({
                "id": r.get("videoId"),
                "title": r.get("title"),
                "artist": ", ".join([a.get("name") for a in r.get("artists", [])]),
                "album": r.get("album", {}).get("name") if r.get("album") else "",
                "duration": r.get("duration"),
                "thumbnail": (r.get("thumbnails") or [{}])[0].get("url", ""),
            })
        return jsonify(songs)
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# ── /api/music/playlists ──────────────────────────────────────────────────────

@app.route("/api/music/playlists", methods=["GET"])
def playlists():
    try:
        my_playlists = yt_music.get_library_playlists()
        res = [{
            "id": p.get("playlistId"),
            "title": p.get("title"),
            "count": p.get("trackCount"),
            "thumbnail": (p.get("thumbnails") or [{}])[0].get("url", ""),
        } for p in my_playlists]
        return jsonify(res)
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# ── /api/music/radio — watch playlist / autoplay seed ────────────────────────

@app.route("/api/music/radio", methods=["GET"])
def music_radio():
    video_id = request.args.get("videoId") or request.args.get("video_id")
    if not video_id:
        return jsonify({"error": "missing videoId"}), 400
    try:
        tracks = _get_related_tracks(video_id)
        return jsonify({"tracks": tracks})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# ── /api/music/moods — mood/genre categories ──────────────────────────────────

@app.route("/api/music/moods", methods=["GET"])
def music_moods():
    try:
        categories = yt_music.get_mood_categories()
        result = []
        for category_name, playlists_list in categories.items():
            items = []
            for p in playlists_list:
                items.append({
                    "params": p.get("params"),
                    "title": p.get("title"),
                    "thumbnail": (p.get("thumbnails") or [{}])[0].get("url", ""),
                })
            result.append({"category": category_name, "playlists": items})
        return jsonify(result)
    except Exception as e:
        return jsonify({"error": str(e)}), 500


# ── Universální now-playing lookup ───────────────────────────────────────────
#
# 4 skupiny českých rádií, každá s vlastním API:
#   1. Active Radio (Evropa 2, Frekvence 1, Dance Radio, Bonton)
#   2. Play.cz / Impuls (Impuls, Rock Rádio, Beat, Country, ...)
#   3. Český rozhlas (Radiožurnál, Dvojka, Vltava, Plus, Wave, Jazz, Junior)
#   4. radia.cz global (Blaník, Fajn, Hitrádio Černá Hora, City, ...)
#
# Matching: icy-name ze stream headeru → normalizované porovnání názvů.
# Cache: TTL 25s na URL (pod 30s polling interval frontendu).

# ── Triton Digital: confirmed working (Evropa 2, Dance Radio) ─────────────────
# Ostatní skupiny (Active Radio, Play.cz) mají privátní API — domény neexistují.
_TRITON_MOUNTS: dict[str, str] = {
    "evropa 2":    "EVROPA2",
    "dance radio": "DANCEAAC",
}

# ── Český rozhlas (veřejnoprávní, stabilní API) ───────────────────────────────
_ROZHLAS: dict[str, str] = {
    "radiožurnál":       "radiozurnal",
    "rádio radiožurnál": "radiozurnal",
    "radiozurnal":       "radiozurnal",
    "rádio dvojka":      "dvojka",
    "dvojka":            "dvojka",
    "rádio vltava":      "vltava",
    "rádio plus":        "plus",
    "radio wave":        "wave",
    "čro jazz":          "jazz",
    "rádio junior":      "junior",
    "junior":            "junior",
}

# ── TTL cache pro API calls ───────────────────────────────────────────────────
_API_CACHE_TTL = 25  # sekund
_api_cache: dict[str, tuple[float, str]] = {}  # url → (ts, content)


# ── Helpers ───────────────────────────────────────────────────────────────────

def _norm(s: str) -> str:
    """Lowercase + odstraní diakritiku."""
    nfkd = unicodedata.normalize("NFD", s.lower().strip())
    return "".join(c for c in nfkd if unicodedata.category(c) != "Mn")


def _simple_get(url: str, timeout: float = 5.0) -> str:
    """HTTP GET s User-Agent."""
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    return urllib.request.urlopen(req, timeout=timeout).read().decode("utf-8", errors="replace")


def _cached_get(url: str) -> str:
    """HTTP GET s TTL cache."""
    now = time.time()
    if url in _api_cache:
        ts, content = _api_cache[url]
        if now - ts < _API_CACHE_TTL:
            return content
    content = _simple_get(url)
    _api_cache[url] = (now, content)
    return content


def _parse_title(data: object, depth: int = 0) -> str:
    """
    Flexibilní parser JSON → 'Artist - Title'.
    Zkouší různé field names (artist/interpret, title/song/track/...)
    a rekurzivně prochází nested objekty.
    """
    if depth > 4:
        return ""
    if isinstance(data, list):
        return _parse_title(data[0], depth) if data else ""
    if not isinstance(data, dict):
        return ""

    # Rekurzivně zkus nested klíče
    for k in ("data", "current", "onair", "now_playing", "song", "track"):
        if k in data and isinstance(data[k], (dict, list)):
            r = _parse_title(data[k], depth + 1)
            if r:
                return r

    # Artist + Title z různých pojmenování
    artist = next(
        (str(data[k]).strip() for k in ("artist", "interpret", "performer", "autor") if data.get(k)),
        ""
    )
    title = next(
        (str(data[k]).strip() for k in ("title", "song", "track", "name", "nazev", "skladba", "titulek") if data.get(k)),
        ""
    )
    if title:
        return f"{artist} - {title}" if artist else title

    # Kombinovaný string field
    for k in ("text", "playing", "description", "display"):
        v = data.get(k)
        if v and isinstance(v, str) and v.strip():
            return v.strip()

    return ""


def _fetch_api_title(url: str, label: str) -> str:
    """GET URL → parse JSON → vrátí 'Artist - Title' nebo ''."""
    try:
        return _parse_title(json.loads(_cached_get(url)))
    except Exception as e:
        print(f"[Bridge] {label} error: {e}", flush=True)
    return ""


def _get_triton_title(mount: str) -> str:
    """Triton Digital XML API — potvrzeno funkční."""
    try:
        import xml.etree.ElementTree as ET
        url = f"https://np.tritondigital.com/public/nowplaying?mountName={mount}&numberToFetch=1&eventType=track"
        root = ET.fromstring(_cached_get(url))
        for info in root.findall(".//nowplaying-info"):
            props = {p.get("name"): (p.text or "").strip() for p in info.findall("property")}
            artist = props.get("track_artist_name", "")
            title  = props.get("cue_title", "") or props.get("track_title", "")
            if title:
                return f"{artist} - {title}" if artist else title
    except Exception as e:
        print(f"[Bridge] Triton/{mount} error: {e}", flush=True)
    return ""


def _get_rozhlas_title(station_id: str) -> str:
    return _fetch_api_title(
        f"https://api.rozhlas.cz/v2/current/radio/{station_id}.json",
        f"ČRo/{station_id}"
    )


def _guess_radia_slugs(icy_name: str) -> list[str]:
    """Odhadne kandidáty na radia.cz slug z názvu stanice (fallback)."""
    base = _norm(icy_name).replace(" ", "-")
    candidates = [base]
    for prefix in ("hitradio-", "hitrádio-", "radio-", "rádio-"):
        if base.startswith(prefix):
            candidates.append(base[len(prefix):])
    for suffix in ("-fm", "-radio", "-rádio", "-1", "-2"):
        if base.endswith(suffix):
            candidates.append(base[:-len(suffix)])
    seen: set[str] = set()
    result = []
    for c in candidates:
        if c and c not in seen:
            seen.add(c)
            result.append(c)
    return result


def _get_radia_cz_slug_title(slug: str) -> str:
    """radia.cz playlist pro konkrétní slug (fallback)."""
    try:
        # Očisti slug od nečistých znaků (corrupted encoding)
        clean_slug = slug.encode("ascii", errors="ignore").decode("ascii")
        if not clean_slug:
            return ""
        content = _cached_get(f"https://2023api.radia.cz/radia/{clean_slug}/playlist")
        try:
            data  = json.loads(content)
            songs = data.get("songs") or data.get("playlist") or []
            cp    = (songs[0] if songs else None) or data.get("currentlyPlaying")
            if cp:
                artist = str(cp.get("artist", "")).strip()
                title  = str(cp.get("title",  "")).strip()
                if title:
                    return f"{artist} - {title}" if artist else title
        except (json.JSONDecodeError, IndexError, AttributeError):
            pass
        m = re.search(r'"artist"\s*:\s*"([^"]+)"[^}]{0,200}"title"\s*:\s*"([^"]+)"', content, re.DOTALL)
        if m:
            return f"{m.group(1).strip()} - {m.group(2).strip()}"
    except urllib.error.HTTPError:
        pass
    except Exception as e:
        print(f"[Bridge] radia.cz slug/{slug} error: {e}", flush=True)
    return ""


def _lookup_now_playing(icy_name: str) -> str:
    """
    Universální hledání aktuálně hrající skladby podle icy-name stanice.

    Pořadí zdrojů (potvrzeno funkční):
      1. Triton Digital    — Evropa 2 (EVROPA2), Dance Radio (DANCEAAC)
      2. Český rozhlas     — Radiožurnál, Dvojka, Vltava, Plus, Wave, Jazz, Junior
      3. radia.cz slug     — Blaník, Fajn, Černá Hora, City, Orion, Beat, ...

    Matching: normalizovaný partial match (bez diakritiky, lowercase).
    Cache:    TTL 25s na každé URL.
    """
    if not icy_name:
        return ""

    norm = _norm(icy_name)

    # 1. Triton Digital (Evropa 2, Dance Radio)
    for known, mount in _TRITON_MOUNTS.items():
        if _norm(known) in norm or norm in _norm(known):
            t = _get_triton_title(mount)
            if t:
                print(f"[Bridge] ✓ Triton '{icy_name}' → '{t}'", flush=True)
                return t

    # 2. Český rozhlas (veřejnoprávní API)
    for known, station_id in _ROZHLAS.items():
        if _norm(known) in norm or norm in _norm(known):
            t = _get_rozhlas_title(station_id)
            if t:
                print(f"[Bridge] ✓ ČRo '{icy_name}' → '{t}'", flush=True)
                return t

    # 3. radia.cz slug (Blaník, Fajn, Černá Hora, City, ...)
    for slug in _guess_radia_slugs(icy_name):
        t = _get_radia_cz_slug_title(slug)
        if t:
            print(f"[Bridge] ✓ radia.cz '{icy_name}' ({slug}) → '{t}'", flush=True)
            return t

    return ""


# ── ICY metadata helper — raw TCP, podporuje HTTP i Shoutcast v1 (ICY 200 OK) ──

def _fetch_icy_title(stream_url: str, timeout: float = 8.0, _depth: int = 0) -> dict:
    """
    Čte ICY StreamTitle z rádiového streamu přes raw TCP socket.
    Funguje pro:
      - HTTP/1.x 200 OK  (Icecast, moderní Shoutcast v2)
      - ICY 200 OK        (Shoutcast v1 — urllib to neumí!)
    Rekurzivně sleduje HTTP redirecty (max 3×).
    """
    if _depth > 3:
        return {"title": "", "error": "too many redirects"}

    parsed = urlparse(stream_url)
    scheme = parsed.scheme.lower()
    if scheme not in ("http", "https"):
        return {"title": "", "error": f"unsupported scheme: {scheme}"}

    host = parsed.hostname or ""
    port = parsed.port or (443 if scheme == "https" else 80)
    path = (parsed.path or "/") + (f"?{parsed.query}" if parsed.query else "")

    request_bytes = (
        f"GET {path} HTTP/1.0\r\n"
        f"Host: {host}\r\n"
        f"Icy-MetaData: 1\r\n"
        f"User-Agent: Mozilla/5.0 (compatible; IcyReader/1.0)\r\n"
        f"Accept: audio/mpeg, audio/ogg, audio/aac, */*\r\n"
        f"Connection: close\r\n\r\n"
    ).encode()

    raw_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    raw_sock.settimeout(timeout)
    try:
        raw_sock.connect((host, port))
        conn = ssl.create_default_context().wrap_socket(raw_sock, server_hostname=host) \
               if scheme == "https" else raw_sock
        conn.sendall(request_bytes)

        buf = b""

        def read_n(n: int) -> bytes:
            nonlocal buf
            while len(buf) < n:
                chunk = conn.recv(4096)
                if not chunk:
                    break
                buf += chunk
            result, buf = buf[:n], buf[n:]
            return result

        def read_headers() -> bytes:
            nonlocal buf
            while b"\r\n\r\n" not in buf and len(buf) < 65536:
                chunk = conn.recv(4096)
                if not chunk:
                    break
                buf += chunk
            idx = buf.find(b"\r\n\r\n")
            if idx < 0:
                result, buf = buf, b""
            else:
                result, buf = buf[:idx + 4], buf[idx + 4:]
            return result

        # ── Parsuj response headers ──────────────────────────────────────
        raw_hdrs = read_headers().decode("utf-8", errors="replace")
        lines = raw_hdrs.split("\r\n")
        status = lines[0] if lines else ""

        print(f"[Bridge] ICY status='{status}' url={stream_url[:50]}")

        # Přijmi HTTP/1.x i ICY
        if not (status.startswith("HTTP/") or status.upper().startswith("ICY")):
            print(f"[Bridge] ICY BAD STATUS: '{status[:120]}'")
            return {"title": "", "error": f"unexpected response: {status[:80]}"}

        hdrs: dict = {}
        for line in lines[1:]:
            if ":" in line:
                k, _, v = line.partition(":")
                hdrs[k.strip().lower()] = v.strip()

        icy_name = hdrs.get("icy-name", "")
        print(f"[Bridge] ICY metaint={hdrs.get('icy-metaint','MISSING')} name='{icy_name}'")

        # ── HTTP redirect ────────────────────────────────────────────────
        code = status.split(" ")[1] if " " in status else ""
        if code in ("301", "302", "303", "307", "308"):
            loc = hdrs.get("location", "")
            print(f"[Bridge] ICY redirect {code} → {loc}")
            if loc:
                return _fetch_icy_title(loc, timeout, _depth + 1)
            return {"title": "", "error": "redirect without Location header"}

        # ── Čti ICY metaint ──────────────────────────────────────────────
        try:
            metaint = int(hdrs.get("icy-metaint", "0").strip())
        except (ValueError, TypeError):
            metaint = 0

        if metaint <= 0:
            print(f"[Bridge] ICY no metaint (={metaint}) for {stream_url[:50]}")
            title = _lookup_now_playing(icy_name)
            return {"title": title, "raw": "", "station": icy_name}

        # Přeskočíme audio data (metaint bytů) → dostaneme se k metadata bloku
        read_n(metaint)

        # 1 byte = délka metadata bloku (× 16)
        len_byte = read_n(1)
        if not len_byte:
            print(f"[Bridge] ICY stream ended before metadata for {stream_url[:50]}")
            return {"title": "", "error": "stream ended before metadata length"}

        meta_len = ord(len_byte) * 16
        if meta_len <= 0:
            print(f"[Bridge] ICY meta_len=0 for {stream_url[:50]}")
            return {"title": "", "raw": ""}

        # Čti metadata a parsuj StreamTitle
        meta_str = read_n(meta_len).decode("utf-8", errors="replace").strip("\x00").strip()
        m = re.search(r"StreamTitle='([^']*)'", meta_str)
        title = m.group(1).strip() if m else ""

        # StreamTitle prázdný → zkus Triton / radia.cz podle icy-name
        if not title:
            title = _lookup_now_playing(icy_name)

        print(f"[Bridge] ICY OK {stream_url[:50]} → '{title}'", flush=True)
        return {"title": title, "raw": meta_str, "station": icy_name}

    finally:
        raw_sock.close()


# ── /api/radio/now-playing ────────────────────────────────────────────────────

@app.route("/api/radio/now-playing", methods=["GET"])
def radio_now_playing():
    stream_url = request.args.get("url")
    if not stream_url:
        return jsonify({"error": "missing url"}), 400
    try:
        result = _fetch_icy_title(stream_url)
        return jsonify(result)
    except Exception as e:
        print(f"[Bridge] ICY error for {stream_url}: {e}")
        return jsonify({"title": "", "error": str(e)})


# ── /api/music/playlist/<id> — tracklist jednoho playlistu ───────────────────

@app.route("/api/music/playlist/<playlist_id>", methods=["GET"])
def get_playlist(playlist_id: str):
    """
    Vrátí tracklist playlistu.
    Limit 50 skladeb — pro LARA frontu stačí, víc by stáhlo příliš dat.
    """
    try:
        result = yt_music.get_playlist(playlist_id, limit=50)
        tracks = []
        for t in result.get("tracks") or []:
            vid = t.get("videoId")
            if not vid:
                continue
            artists = t.get("artists") or []
            thumbs  = t.get("thumbnails") or []
            tracks.append({
                "videoId":   vid,
                "title":     t.get("title", ""),
                "artist":    ", ".join(a.get("name", "") for a in artists),
                "duration":  t.get("duration"),
                "thumbnail": thumbs[-1].get("url", "") if thumbs else "",
            })
        return jsonify({
            "id":         playlist_id,
            "title":      result.get("title", ""),
            "trackCount": len(tracks),
            "tracks":     tracks,
        })
    except Exception as e:
        print(f"[Bridge] get_playlist {playlist_id} ERR: {e}")
        return jsonify({"error": str(e)}), 500


# ── /api/music/mood-tracks — tracklist pro náladu/žánr ───────────────────────

@app.route("/api/music/mood-tracks", methods=["GET"])
def mood_tracks():
    """
    Vrátí tracklist pro daný mood/genre parametr z get_mood_content.
    ?params=<encoded params string> — hodnota z /api/music/moods
    """
    params = request.args.get("params")
    if not params:
        return jsonify({"error": "missing params"}), 400
    try:
        result = yt_music.get_mood_content(params)
        tracks = []
        for t in result or []:
            vid = t.get("videoId")
            if not vid:
                continue
            artists = t.get("artists") or []
            thumbs  = t.get("thumbnails") or []
            tracks.append({
                "videoId":   vid,
                "title":     t.get("title", ""),
                "artist":    ", ".join(a.get("name", "") for a in artists),
                "duration":  t.get("duration"),
                "thumbnail": thumbs[-1].get("url", "") if thumbs else "",
            })
        return jsonify({"tracks": tracks})
    except Exception as e:
        print(f"[Bridge] mood_tracks ERR: {e}")
        return jsonify({"error": str(e)}), 500


# ── /api/status ───────────────────────────────────────────────────────────────

@app.route("/api/status", methods=["GET"])
def status():
    with _sessions_lock:
        session_count = len(_sessions)
    return jsonify({"ok": True, "sessions": session_count})


# ── Main ──────────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=8282, threaded=True)

FROM python:3.11-slim

# ffmpeg is required for transcoding YouTube audio to an MP3 stream LARA can play
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

RUN pip install --no-cache-dir flask ytmusicapi yt-dlp

COPY main.py .

# NOTE: headers.txt (optional YouTube Music Premium cookies) is intentionally
# NOT copied into the image — mount it at runtime instead, see docker-compose.yml.
# Never bake credentials into a Docker image.

EXPOSE 8282

CMD ["python", "main.py"]

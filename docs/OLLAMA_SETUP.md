# Setting Up Ollama for Picobot Brain

Picobot Brain uses Ollama to generate text embeddings locally. This means all your data stays on your machine — no cloud APIs needed.

**What you need:** ~300MB of disk space and ~300MB RAM. Works on x86 (PCs, VPS) and ARM (Raspberry Pi).

---

## Option 1: Docker (Recommended)

The easiest path. Works the same on every platform.

### Create `docker-compose.ollama.yml`

```yaml
services:
  ollama:
    image: ollama/ollama
    container_name: ollama
    ports:
      - "11434:11434"
    volumes:
      - ollama-data:/root/.ollama
    restart: unless-stopped

volumes:
  ollama-data:
```

### Start it and pull the model

```bash
docker compose -f docker-compose.ollama.yml up -d

# Pull the embedding model (one-time, ~274MB)
docker exec ollama ollama pull nomic-embed-text
```

### Verify it works

```bash
curl http://localhost:11434/api/tags
```

You should see `nomic-embed-text` in the list. Done.

---

## Option 2: Install Directly

### Linux (x86 or ARM — Raspberry Pi, VPS, etc.)

```bash
curl -fsSL https://ollama.com/install.sh | sh
ollama pull nomic-embed-text
```

That's it. Ollama starts as a systemd service automatically.

### macOS

```bash
brew install ollama
ollama serve &
ollama pull nomic-embed-text
```

---

## Option 3: Raspberry Pi with GPU (Optional)

If you're on a Pi 5 with an AI kit (Hailo-8L NPU), Ollama can offload to it:

```bash
# Install with ROCm/Hailo support
curl -fsSL https://ollama.com/install.sh | sh
ollama pull nomic-embed-text
```

For most Pi setups, the CPU handles nomic-embed-text fine — it's only 137M parameters. A Pi 5 embeds a page in ~50ms.

---

## Enable in Picobot

Add this to your `~/.picobot/config.json`:

```json
{
  "brain": {
    "enabled": true,
    "embeddingModel": "nomic-embed-text"
  }
}
```

Picobot auto-detects Ollama at `http://localhost:11434`. If you changed the port, set `ollamaUrl`:

```json
{
  "brain": {
    "enabled": true,
    "embeddingModel": "nomic-embed-text",
    "ollamaUrl": "http://localhost:11434"
  }
}
```

---

## Verify the Whole Stack

```bash
# 1. Check Ollama is running
curl -s http://localhost:11434/api/tags | grep nomic

# 2. Test an embedding
curl http://localhost:11434/api/embed \
  -d '{ "model": "nomic-embed-text", "input": "Hello world" }'

# 3. Start Picobot and check brain status
picobot agent -m "Use brain_status to check the brain"
```

---

## Troubleshooting

| Problem | Fix |
|---|---|
| `Brain: no embedding provider available` | Ollama isn't running or not at port 11434 |
| `ollama pull` fails on Pi | Make sure you're on 64-bit OS (`uname -m` should show `aarch64`) |
| Embeddings are slow on Pi | Normal for Pi 4 (~200ms). Pi 5 is ~50ms. Use FTS5-only if too slow |
| Docker volume filling up | Only one model stored (~274MB). Run `docker exec ollama ollama rm MODEL` to remove unused ones |

---

## Using a Cloud API Instead

If you can't run Ollama (e.g., very constrained device), use a remote API:

```json
{
  "brain": {
    "enabled": true,
    "remoteApiBase": "https://api.openai.com",
    "remoteApiKey": "sk-...",
    "remoteModel": "text-embedding-3-small"
  }
}
```

Or disable embeddings entirely — FTS5 keyword search still works:

```json
{
  "brain": {
    "enabled": true,
    "embeddingDims": 0
  }
}
```

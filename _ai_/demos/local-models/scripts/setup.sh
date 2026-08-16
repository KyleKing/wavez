#!/usr/bin/env bash
# Install ollama, start the server, and pull the two candidate models.
set -euo pipefail
brew install ollama
nohup ollama serve > "$(dirname "$0")/../logs/ollama-serve.log" 2>&1 &
sleep 3
ollama pull gemma4:12b
ollama pull qwen3:8b
# qwen3-coder:30b-a3b skipped: smallest quant on ollama.com is q4_K_M at 19GB,
# over the ~12GB budget for that model.

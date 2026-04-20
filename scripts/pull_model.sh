#!/usr/bin/env bash
# pull_model.sh — pull an Ollama model with retry, writing status for nexus to read.
#
# Usage: bash scripts/pull_model.sh <model-name>
#
# Status file: ~/.config/nexus/models/<safe-name>.status
#   pulling     — download in progress
#   retry:N     — N-th retry attempt (1-based)
#   done        — completed successfully
#   failed      — all retries exhausted

set -euo pipefail

MODEL="${1:?usage: pull_model.sh <model-name>}"
SAFE="${MODEL//:/_}"
DIR="$HOME/.config/nexus/models"
STATUS="$DIR/$SAFE.status"

mkdir -p "$DIR"

MAX_RETRIES=3
for attempt in $(seq 1 $MAX_RETRIES); do
    echo "pulling" > "$STATUS"
    if ollama pull "$MODEL"; then
        echo "done" > "$STATUS"
        exit 0
    fi
    if [ "$attempt" -lt "$MAX_RETRIES" ]; then
        echo "retry:$attempt" > "$STATUS"
        backoff=$((attempt * 10))
        echo "[pull_model] $MODEL: attempt $attempt failed — retrying in ${backoff}s"
        sleep "$backoff"
    fi
done

echo "failed" > "$STATUS"
echo "[pull_model] $MODEL: all $MAX_RETRIES attempts failed" >&2
exit 1

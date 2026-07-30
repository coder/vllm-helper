#!/usr/bin/env bash
# What systemd actually runs. Sources the generated active.env (assembled by
# switch.sh from config + secrets + the active profile) and launches vllm serve.
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/common.sh"

[ -f "$VH_ACTIVE_ENV" ] || vh_die "no active model config at $VH_ACTIVE_ENV -- run 'vllm-helper switch <profile>' first"
set -a
# shellcheck disable=SC1090
source "$VH_ACTIVE_ENV"
set +a

# VLLM_BIN comes from config.env -- the vllm executable to use (typically
# inside a venv; exec'ing it directly works without activating the venv).
[ -n "${VLLM_BIN:-}" ] || vh_die "VLLM_BIN is not set in config.env"
[ -x "$VLLM_BIN" ] || vh_die "VLLM_BIN ($VLLM_BIN) does not exist or is not executable"

exec "$VLLM_BIN" serve "$MODEL" \
  --port "$PORT" \
  --max-model-len "$MAX_MODEL_LEN" \
  --gpu-memory-utilization "$GPU_MEMORY_UTILIZATION" \
  --api-key "$VLLM_API_KEY" \
  ${EXTRA_ARGS:-}

#!/usr/bin/env bash
# Shared bootstrap for vllm-helper scripts. Source this first; don't execute it.
#
# Resolves the three directories everything else uses (XDG-based, each
# overridable via environment for testing):
#   VH_ROOT        the checkout/install dir (code only, nothing writes here)
#   VH_CONFIG_DIR  user config: config.env, secrets.env, models/*.env profiles
#   VH_STATE_DIR   machine state: generated active.env, active-profile,
#                  coder-ids.env (Coder model-config UUIDs per profile)
#   VH_DATA_DIR    downloaded data: tiktoken encodings
#
# Then vh_load_config sources config.env + secrets.env (exported) and fills
# in defaults, so scripts can rely on PORT etc. being set.

VH_ROOT="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)"
VH_CONFIG_DIR="${VLLM_HELPER_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/vllm-helper}"
VH_STATE_DIR="${VLLM_HELPER_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/vllm-helper}"
VH_DATA_DIR="${VLLM_HELPER_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/vllm-helper}"

VH_PROFILES_DIR="$VH_CONFIG_DIR/models"
VH_ACTIVE_ENV="$VH_STATE_DIR/active.env"
VH_ACTIVE_PROFILE_FILE="$VH_STATE_DIR/active-profile"
VH_CODER_IDS="$VH_STATE_DIR/coder-ids.env"

vh_die() { echo "vllm-helper: $*" >&2; exit 1; }

vh_load_config() {
  [ -f "$VH_CONFIG_DIR/config.env" ] \
    || vh_die "no config at $VH_CONFIG_DIR/config.env -- run install.sh first"
  set -a
  # shellcheck disable=SC1091
  source "$VH_CONFIG_DIR/config.env"
  [ -f "$VH_CONFIG_DIR/secrets.env" ] && source "$VH_CONFIG_DIR/secrets.env"
  set +a

  PORT="${PORT:-8001}"
  SERVICE_NAME="${SERVICE_NAME:-vllm.service}"
  SERVICE_SCOPE="${SERVICE_SCOPE:-system}"
  MODEL_BUDGET_GB="${MODEL_BUDGET_GB:-90}"
}

# systemctl/journalctl wrappers honoring SERVICE_SCOPE (system needs sudo,
# user scope doesn't).
vh_systemctl() {
  if [ "$SERVICE_SCOPE" = "user" ]; then systemctl --user "$@"; else sudo systemctl "$@"; fi
}
vh_journalctl() {
  if [ "$SERVICE_SCOPE" = "user" ]; then journalctl --user -u "$SERVICE_NAME" "$@"; else journalctl -u "$SERVICE_NAME" "$@"; fi
}

vh_active_profile() { cat "$VH_ACTIVE_PROFILE_FILE" 2>/dev/null || true; }

# coder-ids.env holds one "<profile>=<uuid>" line per profile that has a
# Coder model-config entry. Kept in state (not in the profile files) so
# profiles stay portable/shareable.
vh_get_coder_id() { grep -m1 "^$1=" "$VH_CODER_IDS" 2>/dev/null | cut -d= -f2-; return 0; }
vh_set_coder_id() {
  mkdir -p "$VH_STATE_DIR"
  { grep -v "^$1=" "$VH_CODER_IDS" 2>/dev/null || true; echo "$1=$2"; } > "$VH_CODER_IDS.tmp"
  mv "$VH_CODER_IDS.tmp" "$VH_CODER_IDS"
}
vh_del_coder_id() {
  [ -f "$VH_CODER_IDS" ] || return 0
  { grep -v "^$1=" "$VH_CODER_IDS" || true; } > "$VH_CODER_IDS.tmp"
  mv "$VH_CODER_IDS.tmp" "$VH_CODER_IDS"
}

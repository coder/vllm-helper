#!/usr/bin/env bash
# Installer for vllm-helper. Run from a git clone of the repo:
#
#   git clone https://github.com/coder/vllm-helper && cd vllm-helper
#   ./install.sh                  # system-scope systemd unit (needs sudo)
#   ./install.sh --user           # user-scope unit, no sudo (enables lingering)
#
# Idempotent: safe to re-run after a git pull. Never overwrites existing
# config, secrets, or profiles, and never restarts a running server.
#
# What it does:
#   1. Checks dependencies (bash, curl, python3, systemd; fzf is optional)
#   2. Creates config/state/data dirs (XDG layout)
#   3. Writes config.env with sensible defaults (detects an existing `vllm`
#      binary on PATH; computes a model-size budget from installed memory)
#   4. Generates a random VLLM_API_KEY into secrets.env (chmod 600)
#   5. Copies the curated example profiles (no clobber)
#   6. Downloads the tiktoken vocab files gpt-oss-style models need
#   7. Renders + installs the systemd unit and symlinks vllm-helper onto PATH
set -euo pipefail
cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")"
source lib/common.sh

SCOPE="system"
[ "${1:-}" = "--user" ] && SCOPE="user"

echo "== vllm-helper install ($SCOPE scope) =="
echo

# --- 1. dependencies -------------------------------------------------------
missing=""
for dep in curl python3; do
  command -v "$dep" >/dev/null || missing="$missing $dep"
done
[ -z "$missing" ] || vh_die "missing required dependencies:$missing (install them and re-run)"
command -v systemctl >/dev/null || vh_die "systemd is required (macOS/launchd support is planned but not here yet)"
command -v fzf >/dev/null || echo "note: fzf not found -- 'vllm-helper search' won't work until you install it (everything else will)."

# --- 2. directories --------------------------------------------------------
mkdir -p "$VH_CONFIG_DIR" "$VH_PROFILES_DIR" "$VH_STATE_DIR" "$VH_DATA_DIR/tiktoken-encodings"
chmod 700 "$VH_CONFIG_DIR"

# --- 3. config.env ---------------------------------------------------------
if [ -f "$VH_CONFIG_DIR/config.env" ]; then
  echo "config:   keeping existing $VH_CONFIG_DIR/config.env"
else
  # Find an existing vLLM install rather than assuming one.
  vllm_bin="$(command -v vllm || true)"
  [ -n "$vllm_bin" ] && vllm_bin="$(readlink -f "$vllm_bin")"

  # Rough model-weights budget: ~70% of total memory. Right for
  # unified-memory machines (DGX Spark, GB10); edit MODEL_BUDGET_GB in
  # config.env if you have discrete VRAM.
  total_gb=$(awk '/MemTotal/ {printf "%d", $2/1024/1024}' /proc/meminfo)
  budget_gb=$(( total_gb * 7 / 10 ))

  cat > "$VH_CONFIG_DIR/config.env" <<EOF
# vllm-helper global config. Sourced (exported) into the server's
# environment on every switch -- values here apply to every profile.

# Port the OpenAI-compatible endpoint listens on.
PORT=8001

# Full path to the vllm executable (usually inside a venv). REQUIRED.
VLLM_BIN=$vllm_bin

# How the systemd unit is managed: "system" (sudo systemctl) or "user"
# (systemctl --user). Set by install.sh; re-run install.sh to change it.
SERVICE_SCOPE=$SCOPE
SERVICE_NAME=vllm.service

# Rough budget for model weights on disk, used by 'vllm-helper search' to
# flag models that won't fit. Defaulted to ~70% of total memory
# (${total_gb}GB detected). Lower this if you have discrete VRAM smaller
# than system RAM.
MODEL_BUDGET_GB=$budget_gb

# Where gpt-oss-style models find their tiktoken vocab files.
TIKTOKEN_ENCODINGS_BASE=$VH_DATA_DIR/tiktoken-encodings

# --- Optional: Coder Agents sync -------------------------------------------
# If all three are set (token in secrets.env), every switch keeps a Coder
# deployment's AI-model picker in sync with the active profile. Leave unset
# to disable entirely.
#CODER_URL=https://coder.example.com
#CODER_AI_PROVIDER_ID=
EOF
  echo "config:   wrote $VH_CONFIG_DIR/config.env"
  if [ -z "$vllm_bin" ]; then
    echo
    echo "  WARNING: no 'vllm' executable found on PATH. Install vLLM (e.g. in a"
    echo "  venv: 'uv venv ~/venvs/vllm && uv pip install vllm'), then set"
    echo "  VLLM_BIN in $VH_CONFIG_DIR/config.env to its full path."
    echo
  else
    echo "config:   detected vllm at $vllm_bin"
  fi
fi

# --- 4. secrets.env --------------------------------------------------------
if [ -f "$VH_CONFIG_DIR/secrets.env" ]; then
  echo "secrets:  keeping existing $VH_CONFIG_DIR/secrets.env"
else
  api_key="sk-$(python3 -c 'import secrets; print(secrets.token_urlsafe(32))')"
  cat > "$VH_CONFIG_DIR/secrets.env" <<EOF
# Secrets for vllm-helper. Never commit this file anywhere.

# API key clients must send to the vLLM endpoint (generated at install).
VLLM_API_KEY=$api_key

# Only needed if Coder sync is configured in config.env:
#CODER_API_TOKEN=
EOF
  chmod 600 "$VH_CONFIG_DIR/secrets.env"
  echo "secrets:  generated a new VLLM_API_KEY into $VH_CONFIG_DIR/secrets.env"
fi

# --- 5. example profiles ---------------------------------------------------
copied=0
for f in profiles/*.env; do
  dest="$VH_PROFILES_DIR/$(basename "$f")"
  [ -f "$dest" ] && continue
  cp "$f" "$dest"
  copied=$((copied + 1))
done
echo "profiles: $copied example profile(s) copied to $VH_PROFILES_DIR ($(ls "$VH_PROFILES_DIR" | wc -l) total)"

# --- 6. tiktoken vocab files -----------------------------------------------
for enc in cl100k_base o200k_base; do
  dest="$VH_DATA_DIR/tiktoken-encodings/$enc.tiktoken"
  [ -s "$dest" ] && continue
  echo "tiktoken: downloading $enc.tiktoken..."
  curl -sf "https://openaipublic.blob.core.windows.net/encodings/$enc.tiktoken" -o "$dest" \
    || { echo "tiktoken: download failed (only needed for gpt-oss-style models, continuing)"; rm -f "$dest"; }
done

# --- 7. systemd unit + PATH symlink ----------------------------------------
render_unit() {
  local user_line="$1" wanted_by="$2"
  sed -e "s|@USER_LINE@|$user_line|" \
      -e "s|@ROOT@|$VH_ROOT|g" \
      -e "s|@WANTED_BY@|$wanted_by|" \
      vllm.service.template | grep -v '^#'
}

if [ "$SCOPE" = "user" ]; then
  unit_path="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/vllm.service"
  mkdir -p "$(dirname "$unit_path")"
  render_unit "" "default.target" > "$unit_path"
  systemctl --user daemon-reload
  systemctl --user enable vllm.service >/dev/null 2>&1 || true
  # Keep the server alive after logout.
  loginctl enable-linger "$USER" 2>/dev/null \
    || echo "note: couldn't enable lingering -- the server will stop when you log out (run 'sudo loginctl enable-linger $USER' to fix)."
  echo "systemd:  installed user unit at $unit_path"
else
  unit_path="/etc/systemd/system/vllm.service"
  render_unit "User=$USER" "multi-user.target" | sudo tee "$unit_path" > /dev/null
  sudo systemctl daemon-reload
  sudo systemctl enable vllm.service >/dev/null 2>&1 || true
  echo "systemd:  installed system unit at $unit_path (runs as $USER)"
fi

bin_dir="/usr/local/bin"
if [ -w "$bin_dir" ] || sudo -n true 2>/dev/null; then
  sudo ln -sf "$VH_ROOT/vllm-helper" "$bin_dir/vllm-helper"
  echo "path:     symlinked $bin_dir/vllm-helper"
else
  mkdir -p "$HOME/.local/bin"
  ln -sf "$VH_ROOT/vllm-helper" "$HOME/.local/bin/vllm-helper"
  echo "path:     symlinked ~/.local/bin/vllm-helper (make sure ~/.local/bin is on your PATH)"
fi

echo
echo "== done =="
echo
echo "Next steps:"
echo "  vllm-helper list                  # see the example profiles"
echo "  vllm-helper switch <profile>      # load a model (starts the server)"
echo "  vllm-helper search <query>        # or find something new on HuggingFace"
echo
echo "Clients point at:  http://localhost:$(grep -m1 '^PORT=' "$VH_CONFIG_DIR/config.env" | cut -d= -f2)/v1"
echo "API key:           in $VH_CONFIG_DIR/secrets.env"

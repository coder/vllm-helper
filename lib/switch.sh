#!/usr/bin/env bash
# Library script for vllm-helper. Switches the active vLLM model and restarts
# the service. Only one model is loaded at a time. Not meant to be run
# directly -- use vllm-helper.
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/common.sh"
cd "$VH_ROOT"
vh_load_config

list_profiles() {
  echo "Available profiles ($VH_PROFILES_DIR/*.env):"
  local found=0
  for f in "$VH_PROFILES_DIR"/*.env; do
    [ -e "$f" ] || continue
    found=1
    name=$(basename "$f" .env)
    status=$(grep -m1 '^# Status:' "$f" | sed 's/^# Status: //')
    active=""
    [ "$name" = "$(vh_active_profile)" ] && active=" [active]"
    printf "  %-24s %s%s\n" "$name" "$status" "$active"
  done
  [ "$found" -eq 1 ] || echo "  (none yet -- add one with 'vllm-helper new <org/model>')"
}

usage() {
  list_profiles
  cat <<'EOF'

COMMANDS

  vllm-helper switch <profile>
      Switch to one of the saved profiles listed above. Restarts the server
      and reloads the model -- takes a few minutes.

  vllm-helper new <org/model-name>
      Pull a BRAND-NEW model straight from HuggingFace, save it as a profile,
      and switch to it. See "FINDING NEW MODELS" below for where <org/model-name>
      comes from.

  vllm-helper search <query>
      Search HuggingFace interactively (needs a real terminal, not a plain
      pipe) and pick a model from a live preview pane showing size, license,
      quantization, and whether it fits this machine -- then pulls and
      switches to whatever you pick, same as `new`.

  vllm-helper remove <profile>
      Delete a saved profile (and its Coder model entry, if Coder sync is
      configured). Refuses if it's the one currently active -- switch to
      something else first.

  vllm-helper list
      Just the profile list above, no usage text.

FINDING NEW MODELS

  Every model here is pulled directly from HuggingFace (huggingface.co). The
  <org/model-name> argument to `new` is exactly a HuggingFace repo ID -- copy
  it straight from the URL of a model's page, e.g. the page at
  huggingface.co/Qwen/Qwen3.5-27B has repo ID "Qwen/Qwen3.5-27B".

  Browse available models at: https://huggingface.co/models
  Filter to ones vLLM can actually serve efficiently:
  https://huggingface.co/models?pipeline_tag=text-generation&library=vllm

  Before pulling anything, sanity-check it'll fit on this machine -- see the
  "Adding a brand-new model" section in README.md for the sizing rule of
  thumb.

EXAMPLES

  vllm-helper switch qwen3-coder-next
  vllm-helper new Qwen/Qwen3.5-27B
  vllm-helper remove gpt-oss-120b
EOF
  exit 1
}

[ $# -ge 1 ] || usage

if [ "$1" == "--list" ]; then
  list_profiles
  exit 0
fi

if [ "$1" == "--remove" ]; then
  [ $# -ge 2 ] || usage
  PROFILE="$2"
  PROFILE_FILE="$VH_PROFILES_DIR/$PROFILE.env"
  [ -f "$PROFILE_FILE" ] || { echo "Unknown profile: $PROFILE"; echo; usage; }

  if [ "$PROFILE" == "$(vh_active_profile)" ]; then
    echo "'$PROFILE' is the currently active model -- switch to something else first."
    exit 1
  fi

  lib/sync-coder.sh remove "$PROFILE"
  rm "$PROFILE_FILE"
  echo "Removed profile: $PROFILE"
  exit 0
fi

if [ "$1" == "--new" ]; then
  [ $# -ge 2 ] || usage
  MODEL_REPO="$2"

  # Derive a profile name from the repo id: "Qwen/Qwen3.5-27B" -> "qwen3.5-27b"
  PROFILE=$(echo "$MODEL_REPO" | tr '[:upper:]' '[:lower:]' | sed 's|.*/||')
  PROFILE_FILE="$VH_PROFILES_DIR/$PROFILE.env"

  if [ -f "$PROFILE_FILE" ]; then
    echo "Profile '$PROFILE' already exists for $MODEL_REPO -- using its saved settings."
    echo "(edit $PROFILE_FILE by hand if you want to change its flags)"
  else
    echo "Pulling new model: $MODEL_REPO"
    echo "Checking the model's README for known vLLM serving flags..."
    detected=$(lib/detect-flags.sh "$MODEL_REPO" 2>/dev/null || true)
    extra_args_line=$(echo "$detected" | grep '^EXTRA_ARGS=' || true)
    detected_len=$(echo "$detected" | grep '^DETECTED_MAX_MODEL_LEN=' | cut -d= -f2 || true)
    max_len="${detected_len:-131072}"

    if [ -n "$extra_args_line" ]; then
      echo "Auto-detected from the model card: $(echo "$extra_args_line" | cut -d= -f2-)"
      status="NOT YET VERIFIED -- flags auto-detected from README"
    else
      extra_args_line="EXTRA_ARGS="
      echo "No vllm serve snippet found in the model's README -- check its HF card by"
      echo "hand and edit $PROFILE_FILE if it needs specific flags."
      status="NOT YET VERIFIED -- no flags auto-detected, check the model card by hand"
    fi

    echo "Saving it as profile '$PROFILE' for next time."
    mkdir -p "$VH_PROFILES_DIR"
    cat > "$PROFILE_FILE" <<EOF
# Status: $status (added via vllm-helper new on $(date +%Y-%m-%d))
MODEL=$MODEL_REPO
MAX_MODEL_LEN=$max_len
GPU_MEMORY_UTILIZATION=0.85
$extra_args_line
EOF
  fi
else
  PROFILE="$1"
  PROFILE_FILE="$VH_PROFILES_DIR/$PROFILE.env"
  [ -f "$PROFILE_FILE" ] || { echo "Unknown profile: $PROFILE"; echo; usage; }
fi

# Assemble the runtime env the service will source: global config + secrets
# + the chosen profile. Written to state, never into the repo or config dirs.
mkdir -p "$VH_STATE_DIR"
{
  grep -v '^#' "$VH_CONFIG_DIR/config.env"
  [ -f "$VH_CONFIG_DIR/secrets.env" ] && grep -v '^#' "$VH_CONFIG_DIR/secrets.env"
  grep -v '^#' "$PROFILE_FILE"
} > "$VH_ACTIVE_ENV"
chmod 600 "$VH_ACTIVE_ENV"
echo "$PROFILE" > "$VH_ACTIVE_PROFILE_FILE"

echo "Switched to profile: $PROFILE"
echo
head -8 "$PROFILE_FILE" | grep '^#' || true

echo
lib/sync-coder.sh activate "$PROFILE"

echo
echo "Restarting $SERVICE_NAME (this reloads the model -- can take several minutes,"
echo "longer if the model isn't cached on disk yet)..."
vh_systemctl restart "$SERVICE_NAME"

echo
echo "Watching boot log -- Ctrl+C to stop watching (the service keeps running):"
vh_journalctl -f -o cat

#!/usr/bin/env bash
# OPTIONAL Coder Agents integration. Keeps Coder Agents' model picker in sync
# with the local vLLM profiles. Does nothing (cleanly) unless CODER_URL,
# CODER_API_TOKEN, and CODER_AI_PROVIDER_ID are all configured.
#
# Each profile gets ONE persistent Coder model-config entry (created once,
# never deleted except via `remove`). Switching just flips enabled/is_default
# -- entries never disappear/reappear, only the active one is usable.
# Profile -> Coder config-id mappings live in state (coder-ids.env), not in
# the profile files, so profiles stay shareable across machines.
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/common.sh"
cd "$VH_ROOT"
vh_load_config

cmd="${1:?usage: vllm-helper coder-sync <activate|register|remove> <profile>}"
profile="${2:?usage: vllm-helper coder-sync <activate|register|remove> <profile>}"

if [ -z "${CODER_URL:-}" ] || [ -z "${CODER_API_TOKEN:-}" ] || [ -z "${CODER_AI_PROVIDER_ID:-}" ]; then
  echo "Coder sync: not configured (CODER_URL/CODER_API_TOKEN/CODER_AI_PROVIDER_ID), skipping."
  exit 0
fi

api() {
  local method="$1" path="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -sf -X "$method" "$CODER_URL/api/experimental/chats/model-configs$path" \
      -H "Content-Type: application/json" -H "Accept: application/json" \
      -H "Coder-Session-Token: $CODER_API_TOKEN" -d "$body"
  else
    curl -sf -X "$method" "$CODER_URL/api/experimental/chats/model-configs$path" \
      -H "Accept: application/json" -H "Coder-Session-Token: $CODER_API_TOKEN"
  fi
}

derive_display_name() {
  # "deepreinforce-ai/Ornith-1.0-35B-FP8" -> "Ornith 1.0 35B FP8"
  echo "$1" | sed 's|.*/||; s/-/ /g'
}

profile_file="$VH_PROFILES_DIR/$profile.env"
[ -f "$profile_file" ] || { echo "vllm-helper coder-sync: no such profile: $profile" >&2; exit 1; }

model=$(grep -m1 '^MODEL=' "$profile_file" | cut -d= -f2-)
context_limit=$(grep -m1 '^MAX_MODEL_LEN=' "$profile_file" | cut -d= -f2-)
display_name=$(derive_display_name "$model")
config_id=$(vh_get_coder_id "$profile")

case "$cmd" in
  activate)
    if [ -z "$config_id" ]; then
      echo "Coder sync: creating model-config for '$profile'..."
      resp=$(api POST "" "{\"ai_provider_id\":\"$CODER_AI_PROVIDER_ID\",\"model\":\"$model\",\"display_name\":\"$display_name\",\"context_limit\":$context_limit,\"enabled\":true,\"is_default\":true}") \
        || { echo "Coder sync: create failed, continuing without it" >&2; exit 0; }
      config_id=$(echo "$resp" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
      vh_set_coder_id "$profile" "$config_id"
    else
      echo "Coder sync: updating model-config for '$profile'..."
      api PATCH "/$config_id" "{\"model\":\"$model\",\"display_name\":\"$display_name\",\"context_limit\":$context_limit,\"enabled\":true,\"is_default\":true}" > /dev/null \
        || { echo "Coder sync: update failed, continuing without it" >&2; exit 0; }
    fi

    for f in "$VH_PROFILES_DIR"/*.env; do
      [ -e "$f" ] || continue
      other="$(basename "$f" .env)"
      [ "$other" == "$profile" ] && continue
      other_id=$(vh_get_coder_id "$other")
      [ -z "$other_id" ] && continue
      api PATCH "/$other_id" '{"enabled":false,"is_default":false}' > /dev/null \
        || echo "Coder sync: failed to disable '$other', continuing" >&2
    done
    echo "Coder sync: '$profile' is now the only enabled/default model in Coder Agents."
    ;;

  remove)
    if [ -n "$config_id" ]; then
      echo "Coder sync: deleting model-config for '$profile'..."
      api DELETE "/$config_id" > /dev/null \
        || echo "Coder sync: delete failed (may already be gone), continuing" >&2
      vh_del_coder_id "$profile"
    fi
    ;;

  register)
    # Create the entry if missing, but disabled -- for backfilling profiles
    # that exist locally but aren't the active model. Does NOT touch any
    # other profile's enabled/is_default state.
    if [ -n "$config_id" ]; then
      echo "Coder sync: '$profile' already has a model-config, leaving as-is."
      exit 0
    fi
    echo "Coder sync: registering (disabled) model-config for '$profile'..."
    resp=$(api POST "" "{\"ai_provider_id\":\"$CODER_AI_PROVIDER_ID\",\"model\":\"$model\",\"display_name\":\"$display_name\",\"context_limit\":$context_limit,\"enabled\":false,\"is_default\":false}") \
      || { echo "Coder sync: register failed, continuing without it" >&2; exit 0; }
    config_id=$(echo "$resp" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
    vh_set_coder_id "$profile" "$config_id"
    ;;

  *)
    echo "usage: vllm-helper coder-sync <activate|register|remove> <profile>" >&2
    exit 1
    ;;
esac

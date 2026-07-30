#!/usr/bin/env bash
# Library script for vllm-helper. Interactive HuggingFace model search ->
# fzf picker with a live detail preview -> hands the chosen repo id to
# switch.sh --new. Not meant to be run directly -- use vllm-helper search.
set -euo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/common.sh"
cd "$VH_ROOT"

command -v fzf >/dev/null || vh_die "search needs fzf installed (apt/brew install fzf)"

query="${*:?usage: vllm-helper search <search terms>}"
cache=$(mktemp /tmp/hf-search-XXXXXX.json)
trap 'rm -f "$cache"' EXIT

echo "Searching HuggingFace for \"$query\"..."
curl -sf "https://huggingface.co/api/models?search=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$query")&filter=text-generation&sort=downloads&direction=-1&limit=40" \
  -o "$cache" || { echo "Search request failed (network or rate limit)."; exit 1; }

count=$(python3 -c "import json; print(len(json.load(open('$cache'))))")
if [ "$count" -eq 0 ]; then
  echo "No text-generation models found for \"$query\". Try different terms."
  exit 0
fi
echo "$count results -- type to filter, arrows to browse, Enter to select, Esc to cancel."

selected=$(python3 -c "
import json
models = json.load(open('$cache'))
for m in models:
    print(f\"{m['id']:<48} {m.get('downloads',0):>10} dl   {m.get('likes',0):>5} likes\")
" | fzf --preview "lib/preview.sh {1} $cache" \
        --preview-window=right:55%:wrap \
        --header="Enter: pull & switch  |  Esc: cancel" \
        --height=90% --layout=reverse --border) || {
  echo "Cancelled."
  exit 0
}

repo=$(echo "$selected" | awk '{print $1}')
[ -n "$repo" ] || { echo "Cancelled."; exit 0; }

echo
echo "Selected: $repo"
exec lib/switch.sh --new "$repo"

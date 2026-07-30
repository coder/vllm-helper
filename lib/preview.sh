#!/usr/bin/env bash
# Library script for vllm-helper. Renders the detail pane for one model in
# the fzf search picker (called by lib/search.sh, not meant to run standalone).
# Usage: preview.sh <repo-id> <search-cache-json>
set -uo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/common.sh"
MODEL_BUDGET_GB=90
if [ -f "$VH_CONFIG_DIR/config.env" ]; then
  vh_load_config
fi

repo="$1"
cache="$2"
meta_file=$(mktemp)
trap 'rm -f "$meta_file"' EXIT

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
dim() { printf '\033[2m%s\033[0m\n' "$1"; }

python3 -c "
import json
with open('$cache') as f:
    models = json.load(f)
for m in models:
    if m['id'] == '$repo':
        with open('$meta_file', 'w') as out:
            json.dump(m, out)
        break
" 2>/dev/null

if [ ! -s "$meta_file" ]; then
  echo "No cached metadata for $repo"
  exit 0
fi

bold "$repo"
echo "https://huggingface.co/$repo"
echo

python3 -c "
import json
with open('$meta_file') as f:
    m = json.load(f)
license = next((t.split(':',1)[1] for t in m.get('tags',[]) if t.startswith('license:')), 'unknown')
quant = [t for t in m.get('tags',[]) if t.lower() in ('fp4','fp8','nvfp4','mxfp4','awq','gptq','int4','int8','8-bit','4-bit','gguf','bf16')]
rows = [
    ('license', license),
    ('pipeline', m.get('pipeline_tag') or 'unknown'),
    ('downloads', str(m.get('downloads', 0))),
    ('likes', str(m.get('likes', 0))),
    ('created', (m.get('createdAt') or '')[:10]),
    ('quant', ', '.join(quant) or 'none detected in tags -- check the model card'),
    ('tags', ', '.join(m.get('tags',[])[:12])),
]
for k, v in rows:
    print(f'  {k:<11}{v}')
"

echo
dim "Fetching size..."
tree=$(curl -sf -m 5 "https://huggingface.co/api/models/$repo/tree/main" 2>/dev/null)
printf '\033[1A\033[2K'
if [ -n "$tree" ]; then
  size_gb=$(echo "$tree" | python3 -c "
import json, sys
files = json.load(sys.stdin)
total = sum(f.get('size',0) for f in files if f['path'].endswith(('.safetensors','.bin','.gguf')))
print(f'{total/1e9:.1f}')
" 2>/dev/null)
  if [ -n "$size_gb" ] && [ "$size_gb" != "0.0" ]; then
    printf '  %-11s%s GB' "size:" "$size_gb"
    if python3 -c "exit(0 if float('$size_gb') <= float('$MODEL_BUDGET_GB') else 1)" 2>/dev/null; then
      printf '  \033[32m(fits under the ~%sGB budget)\033[0m\n' "$MODEL_BUDGET_GB"
    else
      printf '  \033[31m(too big for this machine -- look for a smaller quant)\033[0m\n'
    fi
  else
    echo "  size:      unknown (no weight files found at repo root -- check subfolders)"
  fi
else
  echo "  size:      couldn't fetch (network or rate limit)"
fi

echo
dim "Checking model card for known vLLM serving flags..."
detected=$(lib/detect-flags.sh "$repo" 2>/dev/null)
printf '\033[1A\033[2K'
flags_line=$(echo "$detected" | grep '^EXTRA_ARGS=' | cut -d= -f2- || true)
mml_line=$(echo "$detected" | grep '^DETECTED_MAX_MODEL_LEN=' | cut -d= -f2- || true)
if [ -n "$flags_line" ]; then
  printf '  %-11s\033[32m%s\033[0m\n' "vllm:" "$flags_line"
  [ -n "$mml_line" ] && printf '  %-11s%s (from model card)\n' "" "--max-model-len $mml_line"
  echo "  (this is what 'new'/'search' will auto-fill if you pull this one)"
else
  echo "  vllm:      no vllm serve snippet found in README -- flags would need"
  echo "             to be set by hand after pulling, check the model card"
fi

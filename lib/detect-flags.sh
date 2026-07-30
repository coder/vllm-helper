#!/usr/bin/env bash
# Library script for vllm-helper. Fetches a HuggingFace model's README and
# tries to auto-detect vLLM serving flags from any "vllm serve" snippet in
# it. Prints EXTRA_ARGS="..." and (if found) DETECTED_MAX_MODEL_LEN=... to
# stdout. Prints nothing and exits 0 if no usable snippet was found -- the
# caller should treat that as "couldn't auto-detect, flag for manual check."
#
# Deliberately ignored even if present in the snippet: --tensor-parallel-size,
# --pipeline-parallel-size, --data-parallel-size, --host, --port,
# --served-model-name -- these are deployment-topology-specific (multi-GPU
# setups, custom naming) and wrong to blindly copy onto this single-GPU box.
#
# Also auto-adds --default-chat-template-kwargs {"thinking":true} whenever the
# detected --reasoning-parser is "deepseek_v3" or "poolside_v1" specifically.
# Those two register vllm's raw DeepSeekV3ReasoningParser, which defaults
# thinking=False unless each individual REQUEST passes
# chat_template_kwargs.thinking=true -- when it's false, vLLM silently swaps
# in IdentityReasoningParser (zero parsing), so raw <think>/</think> tags leak
# straight into `content` and `reasoning` stays null. No model's README
# mentions this -- it's a vLLM-internals default, found by reading vLLM's own
# reasoning-parser source, not something grep-from-a-model-card can catch. The
# other ~25 registered reasoning parsers (qwen3, gptoss, kimi_k2, etc.) use
# different classes and don't share this default. See:
# vllm/reasoning/deepseek_v3_reasoning_parser.py and poolside_v1_reasoning_parser.py
set -uo pipefail
repo="${1:?usage: detect-flags.sh <org/model-repo>}"
readme_file=$(mktemp)
trap 'rm -f "$readme_file"' EXIT

curl -sf -m 10 "https://huggingface.co/$repo/raw/main/README.md" -o "$readme_file" 2>/dev/null
[ -s "$readme_file" ] || exit 0

python3 - "$readme_file" <<'PYEOF'
import re, sys

with open(sys.argv[1]) as f:
    text = f.read()

m = re.search(r'vllm serve(?:.*\\\n)*.*', text)
if not m:
    raise SystemExit(0)
snippet = m.group(0)

flags = []
if re.search(r'--enable-auto-tool-choice', snippet):
    flags.append('--enable-auto-tool-choice')
tcp = re.search(r'--tool-call-parser\s+(\S+)', snippet)
if tcp:
    flags.append(f'--tool-call-parser {tcp.group(1)}')
rp = re.search(r'--reasoning-parser\s+(\S+)', snippet)
NEEDS_THINKING_DEFAULT = {'deepseek_v3', 'poolside_v1'}
if rp:
    flags.append(f'--reasoning-parser {rp.group(1)}')
    if rp.group(1) in NEEDS_THINKING_DEFAULT:
        flags.append('--default-chat-template-kwargs {\\"thinking\\":true}')
if re.search(r'--enable-prefix-caching', snippet):
    flags.append('--enable-prefix-caching')
if re.search(r'--trust-remote-code', snippet):
    flags.append('--trust-remote-code')

if flags:
    print('EXTRA_ARGS="' + ' '.join(flags) + '"')

mml = re.search(r'--max-model-len\s+(\d+)', snippet)
if mml:
    print(f'DETECTED_MAX_MODEL_LEN={mml.group(1)}')
PYEOF

#!/usr/bin/env bash
# Library script for vllm-helper. Checks for (and offers to install) the
# small set of build-toolchain packages that vLLM's various JIT-kernel-
# compilation paths need on first use. These aren't model-specific --
# they're needed by the *serving stack* (Triton, FlashInfer, CUTLASS-style
# custom ops), and which exact one bites you depends on which kernel shape a
# given model/config happens to exercise first. We've hit two of these for
# real, reactively, in prior debugging sessions:
#   - python3-dev (Python.h) -- Triton kernel compile, hit with Qwen3-Next
#   - ninja-build            -- FlashInfer sampling kernel JIT, hit with Laguna
# Checking for the whole small set up front beats discovering them one
# crash at a time.
set -uo pipefail
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/common.sh"

echo "vllm-helper doctor"
echo "=================="
echo

missing=()

check() {
  local label="$1" cmd="$2"
  if eval "$cmd" > /dev/null 2>&1; then
    printf "  [ok]      %s\n" "$label"
  else
    printf "  [missing] %s\n" "$label"
    missing+=("$3")
  fi
}

echo "Build toolchain (for vLLM/FlashInfer/Triton JIT kernel compilation):"
check "ninja"                  "command -v ninja"                         "ninja-build"
check "gcc/g++"                "command -v gcc && command -v g++"         "build-essential"
py_ver=$(python3 -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")' 2>/dev/null)
check "python${py_ver}-dev (Python.h)" "find /usr/include/python${py_ver}* -name Python.h 2>/dev/null | grep -q ." "python${py_ver}-dev"

echo
echo "GPU / CUDA:"
check "nvidia-smi"             "command -v nvidia-smi"                    ""
check "nvcc"                   "command -v nvcc"                          ""

echo
echo "Search (for 'vllm-helper search'):"
check "fzf"                    "command -v fzf"                           "fzf"

echo
vh_load_config 2>/dev/null || true
if [ -n "${CODER_URL:-}" ]; then
  echo "Coder sync: configured (CODER_URL=$CODER_URL)"
else
  echo "Coder sync: not configured (optional)"
fi

echo
if [ "${#missing[@]}" -eq 0 ]; then
  echo "Everything looks good."
  exit 0
fi

installable=()
for pkg in "${missing[@]}"; do
  [ -n "$pkg" ] && installable+=("$pkg")
done

if [ "${#installable[@]}" -eq 0 ]; then
  echo "Some checks failed above with no known fix to suggest -- see the"
  echo "output above for details."
  exit 1
fi

echo "Missing packages found. Install now with:"
echo "  sudo apt-get update && sudo apt-get install -y ${installable[*]}"
read -r -p "Run this now? [y/N] " reply
if [[ "$reply" =~ ^[Yy]$ ]]; then
  sudo apt-get update && sudo apt-get install -y "${installable[@]}"
else
  echo "Skipped. Re-run 'vllm-helper doctor' anytime."
fi

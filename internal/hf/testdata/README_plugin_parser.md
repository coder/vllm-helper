# NVIDIA-Nemotron-3-Nano-30B-A3B-BF16

## Deployment

```bash
vllm serve nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B-BF16 \
  --trust-remote-code \
  --max-model-len 262144 \
  --max-num-seqs 8 \
  --tensor-parallel-size 1 \
  --served-model-name model \
  --enable-auto-tool-choice \
  --tool-call-parser qwen3_coder \
  --reasoning-parser-plugin nano_v3_reasoning_parser.py \
  --reasoning-parser nano_v3 \
  --api-key sk-should-never-be-copied
```

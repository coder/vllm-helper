---
license: apache-2.0
pipeline_tag: text-generation
---

# Qwen3-8B-FP8

A quantized checkpoint for fast inference.

## Deployment

Serve it with vLLM:

```bash
vllm serve Qwen/Qwen3-8B-FP8 \
    --max-model-len 131072 \
    --tensor-parallel-size 4 \
    --host 0.0.0.0 \
    --port 8000 \
    --served-model-name qwen3 \
    --enable-auto-tool-choice \
    --tool-call-parser hermes \
    --reasoning-parser deepseek_v3 \
    --enable-prefix-caching
```

## License

Released under Apache 2.0.

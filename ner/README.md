# Aperture NER sidecar

Regexes catch structured data: keys, cards, IBANs, emails. They cannot catch a
person's name or a street address — and that is the difference between a secret
scanner and a DLP gateway. This service closes that gap with a local
token-classification model.

It runs **next to the gateway**, not in it:

- Aperture stays a single static Go binary with no ML runtime linked in.
- Prompt text never leaves your network. The gateway refuses a `NER_URL` that
  is not loopback or a private address unless you set `NER_ALLOW_REMOTE=true`.
- You can replace this service with your own — Presidio, GLiNER, spaCy, an
  internal model — as long as it speaks the contract below.

## Contract

```
POST /scan   {"texts": ["Ask Ivan Petrov about it", "..."]}
→ 200        {"results": [{"spans": [{"start": 4, "end": 15,
                                      "label": "PER", "score": 0.99}]}, {...}]}

POST /scan   {"text": "Ask Ivan Petrov about it"}     → {"spans": [...]}
GET  /health                                          → {"status": "ok", ...}
```

`start`/`end` are **byte offsets** into the text, on character boundaries.
Labels are passed through; the gateway acts on `PERSON`/`PER`, `ADDRESS`,
`LOCATION`/`LOC` by default (`NER_LABELS` changes that) and maps them to rule
names — `ner:person`, `ner:address`, `ner:location` — in the incident feed.
Spans below `NER_MIN_SCORE`, outside the text, or splitting a character are
dropped by the gateway, so a buggy service cannot corrupt traffic.

## Run it

```bash
docker compose --profile ner up -d      # from the repo root: gateway + this service
```

Or standalone:

```bash
docker build -t aperture-ner ner/
docker run -p 8081:8081 aperture-ner
curl -s localhost:8081/scan -H 'Content-Type: application/json' \
  -d '{"text":"Позвони Ивану Петрову по адресу Тверская 7"}' | jq
```

Then point the gateway at it and turn the stage on for a policy:

```bash
export NER_URL=http://localhost:8081
curl -X PUT localhost:8080/admin/policies/default \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H 'Content-Type: application/json' \
  -d '{"secrets":"block","pii":"redact","custom":"alert","ner":true}'
```

## The model

Default: [`Babelscape/wikineural-multilingual-ner`](https://huggingface.co/Babelscape/wikineural-multilingual-ner)
— BERT-base fine-tuned for NER in nine languages, English and Russian among
them. It is exported to ONNX at image build time; the running image carries
onnxruntime, tokenizers and numpy, not torch.

Swap it for any token-classification model with a fast tokenizer:

```bash
docker build --build-arg MODEL=<hf-model-id> -t aperture-ner ner/
```

## Settings

| Variable | Meaning |
|----------|---------|
| `PORT` | Listen port (default `8081`) |
| `MODEL_DIR` | Where the exported model lives (default `/model`) |
| `NER_TOKEN` | If set, `Authorization: Bearer <token>` is required |
| `NER_WINDOW_CHARS` / `NER_OVERLAP_CHARS` | Long texts are scanned in overlapping windows (default `1000` / `120`) |
| `NER_MAX_TOKENS` | Tokens per window (default `512`, the model's limit) |
| `NER_BATCH` | Windows per model run (default `16`) |
| `NER_MAX_BODY` | Largest accepted request body (default 2 MiB) |

## Cost

Measured end-to-end through the gateway (Apple M-series, CPU only, int8
weights, one request per measurement):

| Prompt | Added latency |
|--------|---------------|
| 90 B (one sentence) | 26–33 ms |
| 1.3 KB (Russian) | ~140 ms |
| 3.5 KB | 250–410 ms |

The regex path, for comparison, is ~2 ms. Cost grows with prompt length, since
long texts are split into windows — which is why the stage is opt-in per policy
and why `NER_TIMEOUT_MS` defaults to 1000: a tighter budget would silently skip
exactly the long prompts worth scanning. Watch `aperture_ner_latency_seconds`
and `aperture_ner_requests_total{status}` on the gateway's `/metrics`.

The image ships int8 weights (`QUANTIZE=1`, the default), which is 2–3× faster
than full precision and halves the image. Confidence drops a little — a name
scored 0.95 at fp32 scores ~0.87 at int8 — well clear of the 0.5 floor, but
worth knowing if you tune `NER_MIN_SCORE`. Build with `--build-arg QUANTIZE=0`
to keep full precision.

#!/usr/bin/env python3
"""Aperture NER sidecar — local named-entity recognition over HTTP.

Aperture's regex detectors cover structured secrets and PII. Person names and
addresses need a model, and a DLP gateway must not ship prompt text to a cloud
API to get one — so the model runs here, next to the gateway, and the gateway
stays a single static binary.

    POST /scan  {"texts": ["...", "..."]}
    -> 200      {"results": [{"spans": [{"start": 0, "end": 11,
                                         "label": "PER", "score": 0.99}]}, ...]}

    POST /scan  {"text": "..."}      -> {"spans": [...]}      (single-text form)
    GET  /health                     -> {"status": "ok", ...}

Only the Python standard library plus onnxruntime, tokenizers and numpy: no web
framework, so there is little here to audit and little to keep updated.
"""

import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer

MODEL_DIR = os.environ.get("MODEL_DIR", "/model")
PORT = int(os.environ.get("PORT", "8081"))
TOKEN = os.environ.get("NER_TOKEN", "")
# Long prompts are split into overlapping character windows; the overlap keeps
# an entity from being cut in half at a window edge. The window is deliberately
# well under the model's token limit: Cyrillic and CJK produce far more tokens
# per character than English, and a truncated window is a blind spot, not an
# error anyone would notice.
WINDOW = int(os.environ.get("NER_WINDOW_CHARS", "400"))
OVERLAP = int(os.environ.get("NER_OVERLAP_CHARS", "80"))
MAX_TOKENS = int(os.environ.get("NER_MAX_TOKENS", "512"))
BATCH = int(os.environ.get("NER_BATCH", "16"))
# Bytes of text accepted in one request, so one caller cannot pin the CPU.
MAX_BODY = int(os.environ.get("NER_MAX_BODY", str(2 * 1024 * 1024)))
# A one-character "entity" is noise, not personal data.
MIN_SPAN_CHARS = int(os.environ.get("NER_MIN_SPAN_CHARS", "2"))

print(f"loading model from {MODEL_DIR}", flush=True)
session = ort.InferenceSession(
    os.path.join(MODEL_DIR, "model.onnx"), providers=["CPUExecutionProvider"]
)
tokenizer = Tokenizer.from_file(os.path.join(MODEL_DIR, "tokenizer.json"))
tokenizer.enable_truncation(max_length=MAX_TOKENS)
tokenizer.enable_padding(length=None)

with open(os.path.join(MODEL_DIR, "config.json")) as f:
    _config = json.load(f)
ID2LABEL = {int(k): v for k, v in _config["id2label"].items()}
MODEL_NAME = _config.get("_name_or_path", "unknown")
INPUT_NAMES = [i.name for i in session.get_inputs()]
print(f"model ready: {MODEL_NAME}, labels={sorted(set(ID2LABEL.values()))}", flush=True)


def windows(text):
    """Split text into overlapping character windows: [(offset, chunk), ...]."""
    if len(text) <= WINDOW:
        return [(0, text)]
    step = max(1, WINDOW - OVERLAP)
    return [(i, text[i : i + WINDOW]) for i in range(0, len(text), step)]


def softmax(x):
    e = np.exp(x - np.max(x, axis=-1, keepdims=True))
    return e / np.sum(e, axis=-1, keepdims=True)


def run_batch(chunks):
    """Run the model over encoded chunks and return per-chunk (label, score, start, end)."""
    encodings = tokenizer.encode_batch(chunks)
    feed = {}
    ids = np.array([e.ids for e in encodings], dtype=np.int64)
    mask = np.array([e.attention_mask for e in encodings], dtype=np.int64)
    if "input_ids" in INPUT_NAMES:
        feed["input_ids"] = ids
    if "attention_mask" in INPUT_NAMES:
        feed["attention_mask"] = mask
    if "token_type_ids" in INPUT_NAMES:
        feed["token_type_ids"] = np.zeros_like(ids)

    logits = session.run(None, feed)[0]
    probs = softmax(logits)
    best = probs.argmax(axis=-1)

    out = []
    for row, enc in enumerate(encodings):
        # A truncated window means text the model never saw. Say so: silence
        # would look exactly like "nothing found here".
        covered = max((end for _, end in enc.offsets), default=0)
        if covered < len(chunks[row]):
            print(f"warning: window truncated at {covered}/{len(chunks[row])} chars — "
                  f"lower NER_WINDOW_CHARS for this language", file=sys.stderr, flush=True)
        tokens = []
        for pos, (start, end) in enumerate(enc.offsets):
            # Special and padding tokens carry no text.
            if enc.attention_mask[pos] == 0 or start == end:
                continue
            label = ID2LABEL[int(best[row][pos])]
            if label == "O":
                continue
            tokens.append((label, float(probs[row][pos][best[row][pos]]), start, end))
        out.append(tokens)
    return out


def group(tokens):
    """Fold BIO-tagged tokens into entity spans."""
    spans = []
    current = None
    for label, score, start, end in tokens:
        prefix, _, kind = label.partition("-")
        kind = kind or prefix  # models that emit bare labels
        if current and prefix != "B" and kind == current["label"] and start <= current["end"] + 1:
            current["end"] = end
            current["scores"].append(score)
            continue
        if current:
            spans.append(current)
        current = {"label": kind, "start": start, "end": end, "scores": [score]}
    if current:
        spans.append(current)
    return [
        {
            "start": s["start"],
            "end": s["end"],
            "label": s["label"],
            "score": round(sum(s["scores"]) / len(s["scores"]), 4),
        }
        for s in spans
    ]


def snap_to_words(text, spans):
    """Widen each span to whole words.

    Sub-word tokenisation can leave a letter outside the entity — a Russian
    surname comes back as "Петров" with the case ending "у" left behind, so a
    redaction would print "[REDACTED:ner:person]у". Widening only ever removes
    more text, never less, which is the safe direction for DLP.
    """
    for span in spans:
        while span["start"] > 0 and text[span["start"] - 1].isalnum():
            span["start"] -= 1
        while span["end"] < len(text) and text[span["end"]].isalnum():
            span["end"] += 1
    return spans


def to_byte_offsets(text, spans):
    """Convert character offsets to byte offsets.

    The tokenizer reports offsets in characters, but the contract is bytes —
    Go slices strings by byte. On anything non-ASCII (Russian, for one) the two
    differ, and handing over character offsets would redact the wrong range.
    """
    if not spans or text.isascii():
        return spans
    prefix = [0] * (len(text) + 1)
    for i, char in enumerate(text):
        prefix[i + 1] = prefix[i] + len(char.encode("utf-8"))
    for span in spans:
        span["start"] = prefix[span["start"]]
        span["end"] = prefix[span["end"]]
    return spans


def scan(texts):
    """Return the spans for each text, in the same order, in byte offsets."""
    jobs = []  # (text_index, char_offset, chunk)
    for i, text in enumerate(texts):
        if not isinstance(text, str) or not text.strip():
            continue
        for offset, chunk in windows(text):
            jobs.append((i, offset, chunk))

    results = [[] for _ in texts]
    for i in range(0, len(jobs), BATCH):
        batch = jobs[i : i + BATCH]
        for (text_index, offset, _), tokens in zip(batch, run_batch([c for _, _, c in batch])):
            for span in group(tokens):
                span["start"] += offset
                span["end"] += offset
                results[text_index].append(span)

    # Overlapping windows can find the same entity twice.
    for i, spans in enumerate(results):
        seen = set()
        unique = []
        for s in sorted(spans, key=lambda s: (s["start"], -s["end"])):
            key = (s["start"], s["end"], s["label"])
            if key in seen or s["end"] - s["start"] < MIN_SPAN_CHARS:
                continue
            seen.add(key)
            unique.append(s)
        results[i] = to_byte_offsets(texts[i], snap_to_words(texts[i], unique))
    return results


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _send(self, code, payload):
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.rstrip("/") in ("/health", ""):
            self._send(200, {"status": "ok", "model": MODEL_NAME,
                             "labels": sorted(set(ID2LABEL.values()))})
            return
        self._send(404, {"error": "not found"})

    def do_POST(self):
        if self.path.rstrip("/") != "/scan":
            self._send(404, {"error": "not found"})
            return
        if TOKEN and self.headers.get("Authorization", "") != "Bearer " + TOKEN:
            self._send(401, {"error": "unauthorized"})
            return

        length = int(self.headers.get("Content-Length", 0))
        if length > MAX_BODY:
            self._send(413, {"error": "body too large"})
            return
        try:
            payload = json.loads(self.rfile.read(length) or "{}")
        except ValueError:
            self._send(400, {"error": "invalid JSON"})
            return

        single = "texts" not in payload
        texts = [payload.get("text", "")] if single else payload.get("texts") or []
        if not isinstance(texts, list):
            self._send(400, {"error": "texts must be an array"})
            return

        start = time.time()
        try:
            results = scan(texts)
        except Exception as err:  # a model failure must not kill the service
            print(f"scan failed: {err}", file=sys.stderr, flush=True)
            self._send(500, {"error": "scan failed"})
            return
        took = (time.time() - start) * 1000
        print(f"scan: {len(texts)} texts, {sum(len(r) for r in results)} spans, {took:.0f}ms",
              flush=True)

        if single:
            self._send(200, {"spans": results[0] if results else []})
        else:
            self._send(200, {"results": [{"spans": s} for s in results]})

    def log_message(self, *args):
        pass  # requests are logged above, without the text


if __name__ == "__main__":
    print(f"listening on :{PORT}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()

#!/usr/bin/env python3
"""Export a token-classification model to ONNX. Runs once, at image build time.

Plain torch.onnx.export rather than a toolchain on top: fewer moving parts to
pin, and nothing from this stage ends up in the image that runs.
"""
import os
import sys

import torch
from transformers import AutoModelForTokenClassification, AutoTokenizer

model_id = os.environ.get("MODEL", "Babelscape/wikineural-multilingual-ner")
out = os.environ.get("OUT", "/model")
os.makedirs(out, exist_ok=True)

print(f"exporting {model_id}", flush=True)
model = AutoModelForTokenClassification.from_pretrained(model_id).eval()
tokenizer = AutoTokenizer.from_pretrained(model_id)
if not tokenizer.is_fast:
    sys.exit("need a fast tokenizer: the sidecar reads tokenizer.json")

tokenizer.save_pretrained(out)
model.config.save_pretrained(out)

sample = tokenizer("Ivan Petrov lives in Moscow", return_tensors="pt")
names = ["input_ids", "attention_mask"]
args = [sample["input_ids"], sample["attention_mask"]]
if "token_type_ids" in sample:
    names.append("token_type_ids")
    args.append(sample["token_type_ids"])

dynamic = {n: {0: "batch", 1: "sequence"} for n in names}
dynamic["logits"] = {0: "batch", 1: "sequence"}

torch.onnx.export(
    model,
    tuple(args),
    os.path.join(out, "model.onnx"),
    input_names=names,
    output_names=["logits"],
    dynamic_axes=dynamic,
    opset_version=14,
    do_constant_folding=True,
)
print(f"wrote {out}/model.onnx", flush=True)

#!/usr/bin/env python3
"""Quantize the exported model to int8.

A BERT-base forward pass over a long prompt costs hundreds of milliseconds on
a CPU, and agent prompts are long. Dynamic int8 quantization cuts that several
times over for a small accuracy cost — the right trade for a stage that sits in
the request path. Set QUANTIZE=0 at build time to keep full precision.
"""
import os
import shutil

from onnxruntime.quantization import QuantType, quantize_dynamic

model = os.environ.get("OUT", "/model") + "/model.onnx"
full = model.replace(".onnx", ".fp32.onnx")

shutil.move(model, full)
quantize_dynamic(full, model, weight_type=QuantType.QInt8)
os.remove(full)
print(f"quantized {model} to int8", flush=True)

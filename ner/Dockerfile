# The model is exported to ONNX once, at build time, in a stage that carries
# torch. The image that actually runs keeps only onnxruntime, so it is a
# fraction of the size and has no training stack in it.
FROM python:3.12-slim AS export

ARG MODEL=Babelscape/wikineural-multilingual-ner
RUN pip install --no-cache-dir torch==2.5.1 --index-url https://download.pytorch.org/whl/cpu \
 && pip install --no-cache-dir transformers==4.46.3 onnx==1.17.0
COPY export_model.py /export_model.py
RUN MODEL="$MODEL" OUT=/model python /export_model.py && rm -rf /root/.cache

# Int8 weights: several times faster on CPU, which is what a request path
# needs. QUANTIZE=0 keeps full precision.
ARG QUANTIZE=1
COPY quantize_model.py /quantize_model.py
RUN if [ "$QUANTIZE" = "1" ]; then \
      pip install --no-cache-dir onnxruntime==1.20.1 \
   && OUT=/model python /quantize_model.py \
   && rm -rf /root/.cache; \
    fi

FROM python:3.12-slim

RUN pip install --no-cache-dir onnxruntime==1.20.1 tokenizers==0.20.3 numpy==2.1.3

COPY --from=export /model /model
COPY server.py /app/server.py

ENV MODEL_DIR=/model PORT=8081
EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s \
  CMD python -c "import urllib.request;urllib.request.urlopen('http://localhost:8081/health')"
CMD ["python", "/app/server.py"]

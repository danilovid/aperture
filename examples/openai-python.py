# Aperture DLP gateway with the official OpenAI Python SDK.
# The only change vs. talking to OpenAI directly: base_url + your aperture key.
#
#   pip install openai
#   APERTURE_API_KEY=ap-... python openai-python.py

import os

from openai import OpenAI, PermissionDeniedError

client = OpenAI(
    base_url=os.environ.get("APERTURE_URL", "http://localhost:8080") + "/v1",
    api_key=os.environ["APERTURE_API_KEY"],  # aperture key, not a provider key
)

# Clean request — proxied to the provider as usual (works with streaming too).
resp = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Say hi in five words"}],
)
print("clean:", resp.choices[0].message.content)

# A secret in the prompt — Aperture blocks it before it leaves your network.
try:
    client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[{"role": "user", "content": "Deploy with AKIAIOSFODNN7EXAMPLE"}],
    )
except PermissionDeniedError as e:
    print("blocked by DLP:", e.body["message"])

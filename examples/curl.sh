#!/usr/bin/env bash
# Aperture DLP gateway — smoke test with curl.
# Usage: APERTURE_URL=http://localhost:8080 APERTURE_API_KEY=ap-... ./curl.sh
set -euo pipefail

URL="${APERTURE_URL:-http://localhost:8080}"
KEY="${APERTURE_API_KEY:?set APERTURE_API_KEY (printed in the server log at startup)}"

chat() {
  curl -s -w "\nHTTP %{http_code}\n" -X POST "$URL/v1/chat/completions" \
    -H "Authorization: Bearer $KEY" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"gpt-4o-mini\",\"messages\":[{\"role\":\"user\",\"content\":\"$1\"}]}"
}

echo "1) Clean request — passes through to the provider:"
chat "Write a haiku about proxies"

echo
echo "2) AWS key in the prompt — blocked (403), never leaves your network:"
chat "Deploy with AKIAIOSFODNN7EXAMPLE"

echo
echo "3) Email in the prompt — redacted before the provider sees it:"
chat "Contact me at ivan.petrov@corp.io"

echo
echo "Incident feed (requires ADMIN_API_KEY):"
echo "  curl -H \"Authorization: Bearer \$ADMIN_API_KEY\" $URL/admin/dlp/events"

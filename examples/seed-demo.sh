#!/usr/bin/env bash
# Fills the DLP incident feed with varied demo traffic (for screenshots/demos).
# The provider key can be fake — blocked requests never reach the provider,
# and redacted/clean ones just fail upstream, which still records events.
# Usage: APERTURE_API_KEY=ap-... ./seed-demo.sh
set -euo pipefail

URL="${APERTURE_URL:-http://localhost:8080}"
KEY="${APERTURE_API_KEY:?set APERTURE_API_KEY}"

send() { # send <model> <content>
  curl -s -o /dev/null -X POST "$URL/v1/chat/completions" \
    -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
    -d "{\"model\":\"$1\",\"messages\":[{\"role\":\"user\",\"content\":\"$2\"}]}"
}

send gpt-4o-mini        "deploy staging with AKIAIOSFODNN7EXAMPLE right now"
send claude-3-5-sonnet  "here is the token ghp_abcdefghijklmnopqrstuvwxyz0123456789"
send gpt-4o-mini        "ping ivan.petrov@corp.io when the build is green"
send llama-3.3-70b      "customer card 4242 4242 4242 4242 failed the charge"
send gpt-4o-mini        "-----BEGIN RSA PRIVATE KEY----- copy this to the server"
send claude-3-5-sonnet  "transfer to DE89370400440532013000 before friday"
send gpt-4o-mini        "call me at +7 916 123-45-67 about the incident"
send gpt-4o-mini        "slack bot xoxb-123456789012-abcdefABCDEF stopped working"

echo "Seeded 8 DLP events. Open the console → DLP Events, or:"
echo "  curl -H \"Authorization: Bearer \$ADMIN_API_KEY\" $URL/admin/dlp/events"

#!/usr/bin/env bash
# The Jev decision API through Aperture.
#
# Jev (https://www.jevai.org/docs) is not a model: an agent posts business
# fields and gets back a typed decision. The fields are exactly the kind of
# thing that must not leave the network unchecked — a customer message, a tool
# argument, a policy quote — so the gateway scans them first.
#
#   export JEV_API_KEY=...        # from jevai.org/agent/keys, for the gateway
#   export APERTURE_API_KEY=ap-...
#   ./examples/jev.sh
set -euo pipefail

GATEWAY="${GATEWAY:-http://localhost:8080}"
KEY="${APERTURE_API_KEY:?set APERTURE_API_KEY}"

post() {
  curl -sS -X POST "$GATEWAY$1" \
    -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
    -d "$2"
  echo
}

echo "── tool-guard: should a refund tool call go ahead? ───────────────────────"
post /api/v1/decisions/tool-guard '{
  "tool": "issue_customer_refund",
  "action": "Refund USD 680 after a disputed duplicate charge",
  "arguments_summary": ["order_id=ord_7429", "amount_usd=680"],
  "side_effects": ["Moves funds"],
  "policy": ["Refunds above USD 500 require human approval"],
  "reversibility": "partially_reversible"
}'

echo "── the same call with a customer email: redacted before it leaves ────────"
post /api/v1/decisions/tool-guard '{
  "tool": "issue_customer_refund",
  "action": "Refund after a duplicate charge",
  "arguments_summary": ["contact=alice@example.com"]
}'

echo "── a secret in the state: blocked, and Jev is never called ───────────────"
post /api/v1/decisions '{
  "state": {"note": "deploy key AKIAIOSFODNN7EXAMPLE"},
  "questions": {"safe": {"type": "noul", "instructions": "Is this safe to proceed?"}}
}'

echo "The block comes back in Jev's own envelope: code != 0, data null."
echo "Check the incident feed: $GATEWAY/admin/dlp/events?key_id=…"

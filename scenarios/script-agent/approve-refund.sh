#!/bin/sh
# A refund agent in a few lines of shell, for `lagrange tunnel --exec ./approve-refund.sh`.
# It refunds up to 100 right away and asks the sender about anything bigger.
set -eu

if [ "${LAGRANGE_ANSWER:-}" = 1 ]; then
  # The sender answered the question below: {"approved": true|false}.
  if printf '%s' "${LAGRANGE_DATA:-}" | grep -q '"approved": *true'; then
    echo '{"output": "Refund approved and sent.", "status": "refunded"}'
  else
    echo '{"output": "Refund declined.", "status": "declined"}'
  fi
  exit 0
fi

# The request: JSON as text, or as a data part.
req=$(cat)
[ -n "${LAGRANGE_DATA:-}" ] && req=$LAGRANGE_DATA
amount=$(printf '%s' "$req" | sed -n 's/.*"amount": *\([0-9.]*\).*/\1/p')
order=$(printf '%s' "$req" | sed -n 's/.*"order": *"\([^"]*\)".*/\1/p')
[ -n "$amount" ] || { echo 'send {"order": "...", "amount": 42}' >&2; exit 2; }

if [ "${amount%.*}" -le 100 ]; then
  printf '{"output": "Refunded %s for order %s.", "status": "refunded"}\n' "$amount" "$order"
else
  printf '{"ask": "Refund %s for order %s?", "schema": {"type": "object", "properties": {"approved": {"type": "boolean"}}, "required": ["approved"]}}\n' "$amount" "$order"
fi

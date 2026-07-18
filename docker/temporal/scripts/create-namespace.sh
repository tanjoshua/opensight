#!/bin/sh
set -eu

namespace="${TEMPORAL_NAMESPACE:-default}"
attempts=30

while [ "$attempts" -gt 0 ]; do
  if temporal operator namespace describe "$namespace" >/dev/null 2>&1; then
    exit 0
  fi

  if temporal operator namespace create "$namespace" --retention 24h >/dev/null 2>&1; then
    exit 0
  fi

  attempts=$((attempts - 1))
  sleep 2
done

temporal operator namespace create "$namespace" --retention 24h

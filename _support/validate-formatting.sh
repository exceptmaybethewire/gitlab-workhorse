#!/bin/sh

set -eu

IMPORT_RESULT=$(goimports -e -l "$@")

if [ -n "${IMPORT_RESULT}" ]; then
  echo >&2 "Formatting or imports need fixing: 'make fmt'"
  echo "${IMPORT_RESULT}"
  exit 1
fi

#!/bin/sh
# Refresh the vendored vectors from a BBQr checkout (default: sibling clone).
#   testdata/refresh.sh [path/to/BBQr]
set -e
BBQR=${1:-$(dirname "$0")/../../BBQr}
PY=$BBQR/python/ENV/bin/python
cp "$BBQR/test_data/parity-vectors.json" "$(dirname "$0")/parity-vectors.json"
BBQR_REF=$BBQR/python "$PY" "$(dirname "$0")/gen_vectors.py" > "$(dirname "$0")/vectors.json"
"$PY" "$(dirname "$0")/../qr/testdata/gen_tables.py" > "$(dirname "$0")/../qr/tables.go" && gofmt -w "$(dirname "$0")/../qr/tables.go"
"$PY" "$(dirname "$0")/../qr/testdata/gen_vectors.py" > "$(dirname "$0")/../qr/testdata/vectors.json"

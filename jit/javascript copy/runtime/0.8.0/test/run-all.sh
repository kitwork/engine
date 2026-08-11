#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
for file in "$ROOT"/src/*.js "$ROOT"/dist/*.js "$ROOT"/test/*.js; do
  node --check "$file"
done
node "$ROOT/test/expression.test.js"
node "$ROOT/test/lexical-environment.test.js"
node "$ROOT/test/run-conformance.js"
node "$ROOT/test/run-parity.js"

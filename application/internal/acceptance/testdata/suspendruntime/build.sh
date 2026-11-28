#!/bin/sh
set -e
: "${NEURON_ACCEPTANCE_TESTDATA:?acceptance testdata directory must be set}"

out="$PWD/suspendruntime"
cd "$NEURON_ACCEPTANCE_TESTDATA"
# This fixture is an independent module with local replacements; it must not be
# resolved as part of the caller's Go workspace (scripts/script.sh tests inside
# go.work, where this directory would otherwise be attributed to the application
# main module).
GOWORK=off go build -trimpath -buildvcs=false -o "$out" .

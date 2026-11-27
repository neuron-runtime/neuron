#!/bin/sh
set -e
cd "$(dirname "$0")"
go build -trimpath -buildvcs=false -o workerdeadline .

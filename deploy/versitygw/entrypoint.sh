#!/bin/sh
set -e
# A fresh Railway volume is empty; VersityGW's posix backend + sidecar need these.
mkdir -p /data/objects /data/meta
exec /usr/local/bin/versitygw --port :9000 posix --sidecar /data/meta --concurrency 16 /data/objects

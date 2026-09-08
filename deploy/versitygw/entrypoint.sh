#!/bin/sh
set -e
# A fresh Railway volume is empty; VersityGW's posix backend needs its root dir.
# Metadata lives in xattrs: the sidecar layout costs 8 inodes per object.
mkdir -p /data/objects
exec /usr/local/bin/versitygw --port :9000 posix --concurrency 16 /data/objects

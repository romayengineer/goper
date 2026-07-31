#!/bin/sh
set -e

if [ "$(id -u)" = "0" ]; then
    exec su-exec goper /usr/local/bin/goper "$@"
fi

exec /usr/local/bin/goper "$@"

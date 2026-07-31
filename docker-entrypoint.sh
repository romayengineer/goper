#!/bin/sh
set -e

# goper runs as root so it can manage iptables (the container has
# CAP_NET_ADMIN). Its owner-uid iptables rule skips this process's (uid 0)
# traffic, which is why the proxied app must run under a non-root user.
exec /usr/local/bin/goper "$@"

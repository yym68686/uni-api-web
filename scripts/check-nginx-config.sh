#!/bin/sh
set -eu
test_nginx_dir=$(mktemp -d)
trap 'rm -rf "$test_nginx_dir"' EXIT HUP INT TERM
mkdir -p "$test_nginx_dir/logs"
export ANALYTICS_UPSTREAM_SCHEME=http
export ANALYTICS_UPSTREAM_ADDRESS=127.0.0.1:18081
export ANALYTICS_UPSTREAM_PATH=
envsubst '${ANALYTICS_UPSTREAM_SCHEME} ${ANALYTICS_UPSTREAM_ADDRESS} ${ANALYTICS_UPSTREAM_PATH}' < "$1" > "$test_nginx_dir/site.conf"
printf 'events {}\nhttp { include %s/site.conf; }\n' "$test_nginx_dir" > "$test_nginx_dir/nginx.conf"
"${NGINX_BIN:-nginx}" -t -p "$test_nginx_dir" -c "$test_nginx_dir/nginx.conf"

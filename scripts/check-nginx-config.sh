#!/bin/sh
set -eu
test_nginx_dir=$(mktemp -d)
trap 'rm -rf "$test_nginx_dir"' EXIT HUP INT TERM
mkdir -p "$test_nginx_dir/logs"
export ANALYTICS_UPSTREAM_SCHEME=http
export ANALYTICS_UPSTREAM_ADDRESS=127.0.0.1:18081
export ANALYTICS_UPSTREAM_PATH=
filter=${NGINX_ENVSUBST_FILTER:-}
defined_env_names=$(awk -v filter="$filter" 'END { for (name in ENVIRON) if (name ~ filter) print name }' </dev/null | sort | tr '\n' ' ')
expected_env_names='ANALYTICS_UPSTREAM_ADDRESS ANALYTICS_UPSTREAM_PATH ANALYTICS_UPSTREAM_SCHEME '
if [ "$defined_env_names" != "$expected_env_names" ]; then
    echo 'Unexpected NGINX envsubst variable filter' >&2
    exit 1
fi
defined_envs=$(printf '${%s} ' $defined_env_names)
envsubst "$defined_envs" < "$1" > "$test_nginx_dir/site.conf"
printf 'events {}\nhttp { include %s/site.conf; }\n' "$test_nginx_dir" > "$test_nginx_dir/nginx.conf"
"${NGINX_BIN:-nginx}" -t -p "$test_nginx_dir" -c "$test_nginx_dir/nginx.conf"

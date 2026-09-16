#!/usr/bin/env bash
# Runs the access-decision spec against the launcher's copy of
# lua_oidc_full_access.lua. Uses the openresty image only as a LuaJIT host
# (the script is a plain Lua 5.1 chunk; the harness stubs ngx and
# resty.openidc, so nothing is started and nothing is contacted).
#
#   ./tests/proxy-lua/run.sh
#   OPENRESTY_IMAGE=... ./tests/proxy-lua/run.sh
set -euo pipefail

cd "$(dirname "$0")/../.."
IMAGE="${OPENRESTY_IMAGE:-openresty/openresty:alpine}"
LUAJIT=/usr/local/openresty/luajit/bin/luajit

docker run --rm -v "$PWD:/work" -w /work "$IMAGE" \
  "$LUAJIT" tests/proxy-lua/spec_full_access.lua \
  internal/appfiles/proxy/lua_oidc_full_access.lua

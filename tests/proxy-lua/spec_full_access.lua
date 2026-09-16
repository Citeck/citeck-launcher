-- Access-decision tests for internal/appfiles/proxy/lua_oidc_full_access.lua
--
-- Vendored from citeck-devops (docker/nginx/ecos-proxy-oidc/tests/), which owns
-- the upstream copies of this handler. The launcher ships its own copy and
-- overwrites the one inside the ecos-proxy-oidc image at container start
-- (generator_proxy.go), so it needs the same guarantees. Keep the two in sync:
-- the spec is version-aware and runs unmodified against either.
--
-- Every case simulates a completely unauthenticated client (no PA cookie, no
-- Authorization header, no OIDC session). The only thing under test is which
-- identity the script hands to the upstream via the X-ECOS-User header.
--
-- The service exceptions in the script are substring rules over the request
-- URI. They must match the path nginx actually routes and nothing else: not a
-- query parameter, not a path segment in the middle, and not a path that
-- "../" escapes from. See tests/README.md.
--
-- usage: luajit spec_full_access.lua <path-to-lua_oidc_full_access.lua>

local dir = (arg[0]:gsub("[^/]+$", ""))
local harness = dofile(dir .. "harness.lua")
local script = assert(arg[1], "usage: spec_full_access.lua <lua_oidc_full_access.lua>")

local source = assert(io.open(script)):read("*a")
-- a rule is "present" in this config version if the script matches on it,
-- either in its original unanchored form or in the fixed anchored one
local function has(rule)
  return source:find(', "' .. rule .. '")', 1, true) ~= nil
      or source:find(', "^' .. rule .. '")', 1, true) ~= nil
end
local function hasFn(name) return source:find(name, 1, true) ~= nil end
-- A helper that is DEFINED but never called grants nothing, so the public-uri
-- rule is detected by its call site. The launcher's copy of the handler defines
-- is_gateway_public_uri and does not call it.
local function callsGatewayPublicUri()
  return source:find("if is_gateway_public_uri(", 1, true) ~= nil
end

local failures, total, skipped = 0, 0, 0

local function check(name, request, expectedUser)
  total = total + 1
  local ok, res = pcall(harness.run, script, request)
  if not ok then
    failures = failures + 1
    print(string.format("FAIL  %s\n        error: %s", name, tostring(res)))
    return
  end
  if res.user == expectedUser then
    print(string.format("ok    %s", name))
  else
    failures = failures + 1
    print(string.format(
      "FAIL  %s\n        uri      : %s\n        expected : %s\n        actual   : %s (action=%s)",
      name, request.uri, tostring(expectedUser), tostring(res.user), res.action))
  end
end

local PROTECTED = "/gateway/emodel/api/records/query"
local NAVIGATE = { ["Sec-Fetch-Mode"] = "navigate" }

-- Every service exception: the rule as written in the script, a URI that must
-- still get the exception, and the identity it grants.
local exceptions = {
  { rule = '/healthcheck/',            uri = "/healthcheck/monitor.html",        user = "service_healthcheck" },
  { rule = '/rabbitmq',                uri = "/rabbitmq/api/overview",           user = "guest" },
  { rule = '/node%-exporter',          uri = "/node-exporter",                   user = "guest" },
  { rule = '/postgres%-exporter',      uri = "/postgres-exporter",               user = "guest" },
  { rule = '/cadvisor/',               uri = "/cadvisor/containers/",            user = "guest" },
  { rule = '/alfresco/monitoring',     uri = "/alfresco/monitoring/heartbeat",   user = "guest" },
  { rule = '/flowable%-idm/api/idm',   uri = "/flowable-idm/api/idm/users",      user = "guest" },
  { rule = '/flowable%-task/',         uri = "/flowable-task/app",               user = "guest" },
  { rule = '/onlyoffice/',             uri = "/onlyoffice/web-apps/x",           user = "guest" },
  { rule = '/ecos%-idp/',              uri = "/ecos-idp/auth/realms/x",          user = "guest" },
}

print("--- service exceptions still work ---")
for _, e in ipairs(exceptions) do
  if has(e.rule) then
    check(e.uri .. " -> " .. e.user, { uri = e.uri }, e.user)
  else
    skipped = skipped + 1
  end
end
if hasFn("isStaticResUri") then
  check("/main.4f21.js -> guest", { uri = "/main.4f21.js" }, "guest")
  check("/main.js?v=1.2.3 -> guest (cache-busting query)", { uri = "/main.js?v=1.2.3" }, "guest")
else
  skipped = skipped + 2
end
if callsGatewayPublicUri() then
  check("/gateway/emodel/pub/x -> guest", { uri = "/gateway/emodel/pub/x" }, "guest")
else
  skipped = skipped + 1
end
-- nginx decodes the path before routing, so an encoded exception path is the
-- same request and must get the same identity
if has('/healthcheck/') then
  check("/%68ealthcheck/monitor.html -> service_healthcheck (percent-encoded)",
    { uri = "/%68ealthcheck/monitor.html" }, "service_healthcheck")
end

print("\n--- unauthenticated requests to protected endpoints get no identity ---")
check("protected gateway endpoint", { uri = PROTECTED }, nil)
check("protected UI page", { uri = "/v2/dashboard", headers = NAVIGATE }, nil)

print("\n--- a service exception cannot be smuggled in ---")
for _, e in ipairs(exceptions) do
  if has(e.rule) then
    -- put the marker in a query parameter
    check("query string: " .. PROTECTED .. "?probe=" .. e.uri,
      { uri = PROTECTED .. "?probe=" .. e.uri }, nil)
    -- put the marker in the middle of a protected path
    check("mid path: /gateway/emodel" .. e.uri,
      { uri = "/gateway/emodel" .. e.uri }, nil)
    -- start with the real marker path, then traverse out of it (encoded slash
    -- so nginx resolves it late) to a protected gateway endpoint. This is the
    -- strongest vector: the raw request_uri still contains the marker.
    check("traversal: " .. e.uri .. "/..%2f..%2f..%2f..%2f" .. PROTECTED:sub(2),
      { uri = e.uri .. "/..%2f..%2f..%2f..%2f" .. PROTECTED:sub(2) }, nil)
  end
end
if hasFn("isStaticResUri") then
  check("query string cannot fake a static resource extension",
    { uri = "/camunda/app/admin/users?probe=.css" }, nil)
end
if callsGatewayPublicUri() then
  check("traversal out of a gateway public endpoint",
    { uri = "/gateway/emodel/pub/..%2f..%2femodel%2fapi%2frecords%2fquery" }, nil)
end

print(string.format("\n%d/%d passed (%d rules not present in this version)",
  total - failures, total, skipped))
os.exit(failures == 0 and 0 or 1)

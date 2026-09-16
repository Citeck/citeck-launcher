-- Test harness for lua_oidc_full_access.lua
--
-- The access_by_lua_file script is a plain Lua chunk that talks to the nginx
-- `ngx.*` API. We run the *real* file under a stubbed `ngx` so the access
-- decision (which identity ends up in X-ECOS-User) can be asserted offline.
--
-- Run with LuaJIT / Lua 5.1 (setfenv is 5.1-only, same as OpenResty).

local M = {}

local ABORT = {}

-- Runs the given access script for one simulated request.
-- request = { uri = "/path?query", method = "GET", headers = {...}, cookie_PA = "..." }
-- returns { user = <X-ECOS-User or nil>, action = "pass"|"redirect"|"exit", status = <n> }
function M.run(scriptPath, request)

  local result = { action = "pass", headers = {} }
  local headers = request.headers or {}

  local ngx = {
    ERR = "err", INFO = "info", WARN = "warn",
    HTTP_UNAUTHORIZED = 401, HTTP_FORBIDDEN = 403,
    status = 200,
    header = {},
    shared = {},
    var = {
      request_uri = request.uri,
      request_method = request.method or "GET",
      cookie_PA = request.cookie_PA,
      -- $uri is the decoded, normalized path without the query string
      uri = (request.uri:gsub("%?.*$", "")),
      oidc_user = "",
    },
    req = {
      get_headers = function() return headers end,
      set_header = function(name, value) result.headers[name] = value end,
    },
    log = function() end,
    say = function() end,
    exit = function(status) result.action = "exit"; result.status = status; error(ABORT) end,
    redirect = function(to) result.action = "redirect"; result.redirect = to; error(ABORT) end,
  }

  -- Unauthenticated client: no bearer token, no session.
  local openidc = {
    introspect = function() return nil, "no token" end,
    authenticate = function() return nil, "Session not active" end,
  }
  if request.openidc then
    for k, v in pairs(request.openidc) do openidc[k] = v end
  end

  local env = setmetatable({
    ngx = ngx,
    require = function(name)
      if name == "resty.openidc" then return openidc end
      return require(name)
    end,
  }, { __index = _G })

  local chunk = assert(loadfile(scriptPath))
  setfenv(chunk, env)

  local ok, err = pcall(chunk)
  if not ok and err ~= ABORT then error(err) end

  result.user = result.headers["X-ECOS-User"]
  result.status = result.status or ngx.status
  return result
end

return M

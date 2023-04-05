local cjson = require("cjson")
local inspect = require("inspect")
local req = require("resty.ffi.req")

local _M = {}

function _M.get()
    local client, err = req:new_client()
    local ok, res = client:request{
        url = "http://httpbin.local/anything?foo=bar",
        body = "hello",
        args = {
            foo1 = "foo1",
            foo2 = 2,
            foo3 = false,
            foo4 = 2.2,
        },
    }
    assert(ok)
    ngx.say(inspect(res))
    ngx.say(inspect(cjson.decode(res.body)))
end

function _M.post()
    local client, err = req:new_client()
    local ok, res = client:request{
        method = req.HTTP_POST,
        url = "http://httpbin.local/anything",
        body = coroutine.wrap(function()
            coroutine.yield("hello")
        end),
    }
    assert(ok)
    ngx.say(inspect(res))
    ngx.say(inspect(cjson.decode(res.body)))
end

function _M.body_reader()
    local client, err = req:new_client()
    local ok, res = client:request{
        method = req.HTTP_POST,
        url = "http://httpbin.local/anything",
        body = coroutine.wrap(function()
            coroutine.yield("hello")
        end),
        body_reader = true,
    }
    assert(ok)
    ngx.say(inspect(res))

    local body = {}
    for chunk in res.body_reader do
        table.insert(body, chunk)
    end
    ngx.say(inspect(cjson.decode(table.concat(body, ""))))
end

function _M.benchmark_get()
    local urls = {
        http = "http://foo.bar:8080/ip",
        https = "https://foo.bar:9443/ip",
        http2 = "https://foo.bar:8443/ip",
        http3 = "https://foo.bar:8443/ip?http3=true",
    }

    local url = ngx.var.arg_proto and urls[ngx.var.arg_proto] or urls.http
    local cnt = ngx.var.arg_cnt or 10000

    -- lua-resty-ffi-req
    local client, err = req:new_client()
    local cmd = {
        url = url,
    }
    ngx.update_time()
    local t1 = ngx.now()
    for _ = 1,cnt do
        local ok, res = client:request(cmd)
        --ngx.say(inspect(ok))
        --ngx.say(inspect(res))
        --ngx.flush()
        --ngx.sleep(0.1)
        assert(ok and res.status == 200 and res.body == "ok")
    end
    ngx.update_time()
    local t2 = ngx.now()
    ngx.say("lua-resty-ffi-req cnt:", cnt, ", elapsed: ", t2-t1, " secs")
    ngx.flush()

    -- lua-resty-http
    if ngx.var.arg_proto == "http3" then
        url = urls.https
    end
    local http = require"resty.http"
    local httpc, err = http.new()
    assert(err == nil)
    ngx.update_time()
    local t1 = ngx.now()
    for _ = 1,cnt do
        local res, err = httpc:request_uri(url, {ssl_verify=false})
        --ngx.say(inspect(res))
        --ngx.say(inspect(err))
        assert(err == nil and res.status == 200 and res.body == "ok")
    end
    ngx.update_time()
    local t2 = ngx.now()
    ngx.say("lua-resty-http cnt:", cnt, ", elapsed: ", t2-t1, " secs")
end

return _M

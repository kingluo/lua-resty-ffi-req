# lua-resty-ffi-req

The openresty http client library, supports http1, http2 and http3.

It encapsulates golang [req](https://req.cool/) library.

## Background

Check this blog for more detail:

http://luajit.io/posts/http3-client-for-openresty

HTTP2/HTTP3 protocol is popular now,
but OpenResty does not support them.

[req](https://req.cool/) is an intresesing golang library, which provides an unique API
for all HTTP versions.

Highlights:

* automatically detect the server side and select the optimal HTTP version for requests, you can also force the protocol if you want
* Easy Download and Upload (form-data, file)

Why not encapsulate it so that we could reuse it in openresty?

[lua-resty-ffi](https://github.com/kingluo/lua-resty-ffi) provides an efficient and generic API to do hybrid programming
in openresty with mainstream languages (Go, Python, Java, Rust, Nodejs).

`lua-resty-ffi-req = lua-resty-ffi + req`

## Synopsis

```lua
local req = require("resty.ffi.req")
local client, err = req:new_client()
local ok, res = client:request{
    url = "http://httpbin.org/anything?foo=bar",
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

local ok, res = client:request{
    method = req.HTTP_POST,
    url = "http://httpbin.org/anything",
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
```

## Demo

```bash
# install lua-resty-ffi
# https://github.com/kingluo/lua-resty-ffi#install-lua-resty-ffi-via-luarocks
# set `OR_SRC` to your openresty source path
luarocks config variables.OR_SRC /tmp/tmp.Z2UhJbO1Si/openresty-1.21.4.1
luarocks install lua-resty-ffi

cd /opt
git clone https://github.com/kingluo/lua-resty-ffi-req
cd /opt/lua-resty-ffi-req
make

cd /opt/lua-resty-ffi-req/demo

# run nginx
LD_LIBRARY_PATH=/opt/lua-resty-ffi-req:/usr/local/lib/lua/5.1 \
nginx -p $PWD -c nginx.conf

# in another terminal, run demo
curl localhost:20000/demo/get
```

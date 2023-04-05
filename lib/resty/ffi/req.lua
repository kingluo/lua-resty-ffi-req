--
-- Copyright (c) 2023, Jinhua Luo (kingluo) luajit.io@gmail.com
-- All rights reserved.
--
-- Redistribution and use in source and binary forms, with or without
-- modification, are permitted provided that the following conditions are met:
--
-- 1. Redistributions of source code must retain the above copyright notice, this
--    list of conditions and the following disclaimer.
--
-- 2. Redistributions in binary form must reproduce the above copyright notice,
--    this list of conditions and the following disclaimer in the documentation
--    and/or other materials provided with the distribution.
--
-- 3. Neither the name of the copyright holder nor the names of its
--    contributors may be used to endorse or promote products derived from
--    this software without specific prior written permission.
--
-- THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
-- AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
-- IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
-- DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
-- FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
-- DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
-- SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
-- CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
-- OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
-- OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
--
local cjson_encode = require("cjson").encode
local cjson_decode = require("cjson").decode
local base64_decode = ngx.decode_base64
require("resty_ffi")
local req = ngx.load_ffi("resty_ffi_req")

local CMD_NEW_CLIENT = 0
local CMD_CLOSE_CLIENT = 1
local CMD_REQUEST = 2
local CMD_WRITE_REQ_BODY = 3
local CMD_READ_RSP_BODY = 4
local CMD_READ_TRAILER = 5
local CMD_CLOSE_REQUEST = 6

local _M = {
    HTTP_GET = 0,
    HTTP_POST = 1,
    HTTP_PUT = 2,
    HTTP_DELETE = 3,
    HTTP_OPTIONS = 4,
    HTTP_HEAD = 5,
    HTTP_PATCH = 6,
}

local objs = {}

ngx.timer.every(3, function()
    if #objs > 0 then
        for _, s in ipairs(objs) do
            local ok = s:close()
            assert(ok)
        end
        objs = {}
    end
end)

local function setmt__gc(t, mt)
    local prox = newproxy(true)
    getmetatable(prox).__gc = function() mt.__gc(t) end
    t[prox] = true
    return setmetatable(t, mt)
end

local meta_client = {
    __gc = function(self)
        if self.closed then
            return
        end
        table.insert(objs, self)
    end,
    __index = {
        request = function(self, opts)
            local client = self.client
            local body = opts.body
            if type(body) == "function" then
                opts.body = nil
                opts.body_writer = true
            end
            local cmd = {
                cmd = CMD_REQUEST,
                client = client,
                req = opts,
            }
            local ok, res = req:call(cjson_encode(cmd))
            local req_id
            if opts.body_writer then
                req_id = tonumber(res)
                cmd.cmd = CMD_WRITE_REQ_BODY
                cmd.req_id = req_id
                cmd.req = {
                    body = 1,
                }
                for chunk in body do
                    cmd.req.body = chunk
                    local ok, res = req:write_req_body(cjson_encode(cmd))
                    assert(ok)
                    assert(res == nil)
                end
                cmd.req = nil
                ok, res = req:close_req_body(cjson_encode(cmd))
            end
            if ok then
                res = cjson_decode(res)
            end
            if ok and opts.body_reader then
                local req_id = tonumber(res.req_id)
                res.req_id = nil
                res.body_reader = coroutine.wrap(function()
                    local cmd = {
                        cmd = CMD_READ_RSP_BODY,
                        client = client,
                        req_id = req_id,
                    }
                    local ok, res = req:get(cjson_encode(cmd))
                    if ok then
                        if res.body then
                            res.body = base64_decode(res.body)
                        end
                        coroutine.yield(res)
                    else
                        local ok = req:close_req(cjson_encode{
                            cmd = CMD_CLOSE_REQUEST,
                            client = client,
                            req_id = req_id,
                        })
                        assert(ok)
                        return nil, res
                    end
                end)
                res.read_trailers = function()
                    local cmd = {
                        cmd = CMD_READ_TRAILER,
                        client = client,
                        req_id = req_id,
                    }
                    local ok, res = req:get(cjson_encode(cmd))
                    if ok and res then
                        res = cjson_decode(res)
                    end
                    return ok, res
                end
            else
                if req_id then
                    local ok = req:close_req(cjson_encode{
                        cmd = CMD_CLOSE_REQUEST,
                        client = client,
                        req_id = req_id,
                    })
                    assert(ok)
                end
            end
            if ok and res.body then
                res.body = base64_decode(res.body)
            end
            return ok, res
        end,
        close = function(self)
            local ok = req:new(cjson_encode{
                cmd = CMD_CLOSE_CLIENT,
                client = self.client,
            })
            assert(ok)
            self.closed = true
            return ok
        end,
    }
}

function _M.new_client(opts)
    local ok, client = req:new(cjson_encode{
        cmd = CMD_NEW_CLIENT,
    })
    assert(ok)
    if ok then
        return setmt__gc({
            client = tonumber(client),
            closed = false
        }, meta_client)
    else
        return nil, client
    end
end

return _M

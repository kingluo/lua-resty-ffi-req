// Copyright (c) 2023, Jinhua Luo (kingluo) luajit.io@gmail.com
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
//  1. Redistributions of source code must retain the above copyright notice, this
//     list of conditions and the following disclaimer.
//
//  2. Redistributions in binary form must reproduce the above copyright notice,
//     this list of conditions and the following disclaimer in the documentation
//     and/or other materials provided with the distribution.
//
//  3. Neither the name of the copyright holder nor the names of its
//     contributors may be used to endorse or promote products derived from
//     this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
// FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
package main

/*
#cgo LDFLAGS: -shared
#include <string.h>
void* ngx_http_lua_ffi_task_poll(void *p);
char* ngx_http_lua_ffi_get_req(void *tsk, int *len);
void ngx_http_lua_ffi_respond(void *tsk, int rc, char* rsp, int rsp_len);
*/
import "C"
import (
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"unsafe"

	//_ "net/http/pprof"

	"github.com/cristalhq/base64"
	"github.com/imroc/req/v3"
	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	NEW_CLIENT uint = iota
	CLOSE_CLIENT
	REQUEST
	WRITE_REQ_BODY
	READ_RSP_BODY
	READ_TRAILER
	CLOSE_REQUEST
)

const (
	HTTP_GET uint = iota
	HTTP_POST
	HTTP_PUT
	HTTP_DELETE
	HTTP_OPTIONS
	HTTP_HEAD
	HTTP_PATCH
)

type Request struct {
	Method     *int                    `json:"method"`
	URL        string                  `json:"url"`
	Args       *map[string]interface{} `json:"args"`
	Headers    *map[string]string      `json:"headers"`
	Body       *string                 `json:"body"`
	BodyWriter bool                    `json:"body_writer"`
	BodyReader bool                    `json:"body_reader"`
	Form       *map[string]interface{} `json:"form"`
	Files      *map[string]string      `json:"files"`
}

type ClientConfig struct {
	Version            *uint    `json:"version"`
	Workers            *uint    `json:"workers"`
	MaxConnsPerHost    *int     `json:"max_conns_per_host"`
	MaxIdleConns       *int     `json:"max_idle_conns"`
	DisableCompression *bool    `json:"disable_compression"`
	DisableHTTP3       *bool    `json:"disable_http3"`
	DisableH2C         *bool    `json:"disable_h2c"`
	SSLVerify          []string `json:"ssl_verify"`
	SSLServerName      *string  `json:"ssl_server_name"`
}

type Command struct {
	task         unsafe.Pointer `json:"-"`
	Cmd          uint           `json:"cmd"`
	Client       *uint64        `json:"client"`
	ReqId        *uint64        `json:"req_id"`
	ClientConfig *ClientConfig  `json:"client_config"`
	Req          *Request       `json:"req"`
}

type Response struct {
	StatusCode int         `json:"status"`
	ProtoMajor int         `json:"proto_major"`
	ProtoMinor int         `json:"proto_minor"`
	Headers    http.Header `json:"headers"`
	Body       *string     `json:"body,omitempty"`
	ReqId      *uint64     `json:"req_id,omitempty"`
	Trailer    http.Header `json:"trailer,omitempty"`
	TLSVersion *uint16     `json:"tls_version,omitempty"`
}

type BodyCtx struct {
	id        uint64
	reqWriter *io.PipeWriter
	cmd       *Command
	rsp       *req.Response
	rspCh     chan *Command
}

func (ctx *BodyCtx) Close() {
	if ctx.reqWriter != nil {
		ctx.reqWriter.Close()
	}
	if ctx.rspCh != nil {
		close(ctx.rspCh)
	}
}

type Client struct {
	*req.Client
	reqCh  chan *Command
	req_id atomic.Uint64
	reqs   sync.Map
}

func sendReq(r *req.Request, cmd *Command, client *Client, ctx *BodyCtx) {
	var resp *req.Response
	var err error
	if cmd.Req.Method == nil {
		resp, err = r.Get(cmd.Req.URL)
	} else {
		switch *cmd.Req.Method {
		case 1:
			resp, err = r.Post(cmd.Req.URL)
		default:
			resp, err = r.Get(cmd.Req.URL)
		}
	}
	var rc C.int
	var data interface{}
	if err != nil {
		log.Printf("%+v\n", err)
		rc = 1
		data = err
	} else {
		rsp := &Response{
			StatusCode: resp.StatusCode,
			ProtoMajor: resp.ProtoMajor,
			ProtoMinor: resp.ProtoMinor,
			Headers:    resp.Header,
			Trailer:    resp.Trailer,
		}
		if resp.TLS != nil {
			rsp.TLSVersion = &resp.TLS.Version
		}
		data = rsp
		if cmd.Req.BodyReader {
			if ctx != nil {
				ctx.reqWriter = nil
				rsp.ReqId = &ctx.id
			} else {
				id := client.req_id.Add(1)
				ctx = &BodyCtx{id: id, rsp: resp}
				client.reqs.Store(id, ctx)
				rsp.ReqId = &id
			}
			ch := make(chan *Command, 100)
			ctx.rspCh = ch
			go func() {
				b := make([]byte, 0, 512)
				defer func() {
					//FIXME, data race
					ctx.rspCh = nil
					close(ch)
					resp.Body.Close()
				}()
			read_body_loop:
				for cmd := range ch {
					for {
						if len(b) == cap(b) {
							// Add more capacity (let append pick how much).
							b = append(b, 0)[:len(b)]
						}
						sz := cap(b) - len(b)
						n, err := resp.Body.Read(b[len(b):cap(b)])
						b = b[:len(b)+n]
						if err != nil {
							if err != io.EOF {
								log.Printf("%+v\n", err)
							}
							reply(0, b, cmd)
							break read_body_loop
						}
						if n < sz {
							break
						}
					}
					reply(0, b, cmd)
					b = b[:0]
				}
			}()
		} else {
			body, err := resp.ToBytes()
			if err != nil {
				log.Fatal(err)
			}
			str := base64.StdEncoding.EncodeToString(body)
			rsp.Body = &str
		}
	}
	rsp, err := json.Marshal(data)
	if err != nil {
		log.Fatalln(err)
	}
	reply(rc, rsp, cmd)
}

func reply(rc C.int, rsp []byte, cmd *Command) {
	if rsp != nil && len(rsp) > 0 {
		C.ngx_http_lua_ffi_respond(cmd.task, rc, (*C.char)(C.CBytes(rsp)), C.int(len(rsp)))
	} else {
		C.ngx_http_lua_ffi_respond(cmd.task, rc, nil, 0)
	}
}

func handle_request(client *Client, cmd *Command) {
	r := client.R()

	if cmd.Req.Args != nil {
		r.SetQueryParamsAnyType(*cmd.Req.Args)
	}

	if cmd.Req.BodyReader {
		r.DisableAutoReadResponse()
	}

	if !cmd.Req.BodyWriter {
		if cmd.Req.Body != nil {
			r.SetBody(*cmd.Req.Body)
		} else {
			if cmd.Req.Form != nil {
				r.SetFormDataAnyType(*cmd.Req.Form)
			}
			if cmd.Req.Files != nil {
				r.SetFiles(*cmd.Req.Files)
			}
		}
		sendReq(r, cmd, client, nil)
	} else {
		var bodyCtx *BodyCtx
		reqR, reqW := io.Pipe()
		r.SetBody(reqR)
		id := client.req_id.Add(1)
		bodyCtx = &BodyCtx{id: id, reqWriter: reqW, cmd: cmd}
		client.reqs.Store(id, bodyCtx)
		data := strconv.FormatUint(id, 10)
		reply(0, []byte(data), cmd)

		cmd.task = nil
		sendReq(r, cmd, client, bodyCtx)
	}
}

func request(client *Client) {
	for cmd := range client.reqCh {
		handle_request(client, cmd)
	}
}

//export libffi_init
func libffi_init(_ *C.char, tq unsafe.Pointer) C.int {
	//go http.ListenAndServe("localhost:6060", nil)
	go func() {
		var client_idx uint64
		clients := make(map[uint64]*Client)
		for {
			task := C.ngx_http_lua_ffi_task_poll(tq)
			if task == nil {
				log.Println("exit lua-resty-ffi-req runtime")
				break
			}

			var rlen C.int
			r := C.ngx_http_lua_ffi_get_req(task, &rlen)
			data := C.GoBytes(unsafe.Pointer(r), rlen)
			var cmd Command
			err := json.Unmarshal(data, &cmd)
			if err != nil {
				log.Fatalln("error:", err)
			}
			cmd.task = task

			//log.Printf("%+v\n", cmd)
			switch cmd.Cmd {
			case NEW_CLIENT:
				client_idx += 1
				cli := req.C().EnableHTTP3().EnableInsecureSkipVerify()
				var nWorkers uint = 10
				if cmd.ClientConfig != nil {
					cfg := cmd.ClientConfig
					if cfg.Version != nil {
						switch *cfg.Version {
						case 1:
							cli.EnableForceHTTP1()
						case 2:
							cli.EnableForceHTTP2()
						case 3:
							cli.EnableForceHTTP3()
						}
					}
					if cfg.SSLVerify != nil {
						//cli.DisableInsecureSkipVerify()
						certPool, err := x509.SystemCertPool()
						if err != nil {
							log.Fatal(err)
						}
						for _, f := range cfg.SSLVerify {
							caCertRaw, err := os.ReadFile(f)
							if err != nil {
								panic(err)
							}
							if ok := certPool.AppendCertsFromPEM(caCertRaw); !ok {
								panic("Could not add root ceritificate to pool.")
							}
						}
						cfg := cli.GetTLSClientConfig()
						cfg.RootCAs = certPool
					}
					if cfg.SSLServerName != nil {
						tlsCfg := cli.GetTLSClientConfig()
						tlsCfg.ServerName = *cfg.SSLServerName
					}
					if cfg.Workers != nil {
						nWorkers = *cfg.Workers
					}
					if cfg.MaxIdleConns != nil {
						cli.GetTransport().SetMaxIdleConns(*cfg.MaxIdleConns)
					}
					if cfg.MaxConnsPerHost != nil {
						cli.GetTransport().SetMaxConnsPerHost(*cfg.MaxConnsPerHost)
					}
					if cfg.DisableCompression != nil && *cfg.DisableCompression {
						cli.DisableCompression()
					}
					if cfg.DisableHTTP3 != nil && *cfg.DisableHTTP3 {
						cli.DisableHTTP3()
					}
					if cfg.DisableH2C != nil && *cfg.DisableH2C {
						cli.DisableH2C()
					}
				}
				ch := make(chan *Command, 1000)
				client := &Client{Client: cli, reqCh: ch}
				var i uint
				for i = 0; i < nWorkers; i++ {
					go request(client)
				}
				clients[client_idx] = client
				rsp := strconv.FormatUint(client_idx, 10)
				C.ngx_http_lua_ffi_respond(task, 0, (*C.char)(C.CString(rsp)), C.int(len(rsp)))
			case CLOSE_CLIENT:
				idx := *cmd.Client
				client := clients[idx]
				close(clients[idx].reqCh)
				delete(clients, idx)
				client.reqs.Range(func(key, value interface{}) bool {
					ctx := value.(*BodyCtx)
					ctx.Close()
					return true
				})
				C.ngx_http_lua_ffi_respond(task, 0, nil, 0)
			case REQUEST:
				idx := *cmd.Client
				client := clients[idx]
				if len(client.reqCh) > 100 {
					log.Println("all request workers busy, send request directly")
					go handle_request(client, &cmd)
				} else {
					client.reqCh <- &cmd
				}
			case WRITE_REQ_BODY:
				idx1 := *cmd.Client
				idx2 := *cmd.ReqId
				go func() {
					client := clients[idx1]
					item, _ := client.reqs.Load(idx2)
					ctx := item.(*BodyCtx)
					if cmd.Req != nil && cmd.Req.Body != nil {
						ctx.reqWriter.Write([]byte(*cmd.Req.Body))
						C.ngx_http_lua_ffi_respond(task, 0, nil, 0)
					} else {
						ctx.cmd.task = task
						ctx.reqWriter.Close()
					}
				}()
			case READ_RSP_BODY:
				idx1 := *cmd.Client
				idx2 := *cmd.ReqId
				client := clients[idx1]
				item, _ := client.reqs.Load(idx2)
				ctx := item.(*BodyCtx)
				select {
				case ctx.rspCh <- &cmd:
				default:
					C.ngx_http_lua_ffi_respond(task, 0, nil, 0)
				}
			case READ_TRAILER:
				idx1 := *cmd.Client
				idx2 := *cmd.ReqId
				client := clients[idx1]
				item, _ := client.reqs.Load(idx2)
				ctx := item.(*BodyCtx)
				var rsp []byte
				if ctx.rsp.Trailer != nil {
					var err error
					rsp, err = json.Marshal(ctx.rsp.Trailer)
					if err != nil {
						log.Fatalln(err)
					}
				}
				reply(0, rsp, &cmd)
			case CLOSE_REQUEST:
				idx1 := *cmd.Client
				idx2 := *cmd.ReqId
				client := clients[idx1]
				item, _ := client.reqs.Load(idx2)
				ctx := item.(*BodyCtx)
				ctx.Close()
				client.reqs.Delete(idx2)
				C.ngx_http_lua_ffi_respond(task, 0, nil, 0)
			}
		}
	}()

	return 0
}

func main() {}

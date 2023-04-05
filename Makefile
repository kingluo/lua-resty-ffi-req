INST_PREFIX ?= /usr
INST_LIBDIR ?= $(INST_PREFIX)/lib/lua/5.1
INST_LUADIR ?= $(INST_PREFIX)/share/lua/5.1
INSTALL ?= install

.PHONY: build
build:
	go build -buildmode=c-shared -o libresty_ffi_req.so main.go

.PHONY: install
install:
	$(INSTALL) -d $(INST_LUADIR)/resty/ffi_req
	$(INSTALL) lib/resty/ffi_req/*.lua $(INST_LUADIR)/resty/ffi_req
	$(INSTALL) -d $(INST_LIBDIR)/
	$(INSTALL) libffi_go_req.so $(INST_LIBDIR)/

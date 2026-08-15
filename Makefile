BIN     := bin/rent-scout
PKG     := ./cmd/rent-scout
GO      ?= go

.PHONY: all build clean run test vet help

all: build

build:
	@mkdir -p bin
	$(GO) build -o $(BIN) $(PKG)

clean:
	rm -rf bin
	rm -f rent-scout

run: build
	./$(BIN)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

help:
	@echo "make / make build  编译到 $(BIN)"
	@echo "make run           编译并运行"
	@echo "make test          跑全部测试"
	@echo "make vet           go vet"
	@echo "make clean         删除 bin/ 和根目录误编的二进制"

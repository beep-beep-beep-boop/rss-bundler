all: build

build:
	GOFLAGS=-mod=vendor go build -o rssbundler .

run: build
	./rssbundler

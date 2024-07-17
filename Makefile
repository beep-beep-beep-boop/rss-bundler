all: build

build:
	go build .

run: build
	./rssbundler
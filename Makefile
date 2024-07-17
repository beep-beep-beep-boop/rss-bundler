all: build

build:
	go build -o rssbundler .

run: build
	./rssbundler

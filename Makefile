all:
	rm -f bdmultiplex
	CGO_ENABLED=0 go build -ldflags="-extldflags=-static -s -w" -o bdmultiplexer

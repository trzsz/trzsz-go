BIN_DIR := ./bin
BIN_DST := /usr/bin

all: ${BIN_DIR}/trz ${BIN_DIR}/tsz

${BIN_DIR}/trz: $(wildcard ./cmd/trz/*.go ./trzsz/*.go)
	go build -o $@ ./cmd/trz

${BIN_DIR}/tsz: $(wildcard ./cmd/tsz/*.go ./trzsz/*.go)
	go build -o $@ ./cmd/tsz

clean:
	-rm -f ${BIN_DIR}/*

install: all
	cp ${BIN_DIR}/trz ${BIN_DST}/
	cp ${BIN_DIR}/tsz ${BIN_DST}/

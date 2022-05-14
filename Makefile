BIN_DIR := ./bin
BIN_DST := /usr/bin

all: ${BIN_DIR}/trz ${BIN_DIR}/tsz ${BIN_DIR}/trzsz

${BIN_DIR}/trz: $(wildcard ./cmd/trz/*.go ./trzsz/*.go)
	go build -o $@ ./cmd/trz

${BIN_DIR}/tsz: $(wildcard ./cmd/tsz/*.go ./trzsz/*.go)
	go build -o $@ ./cmd/tsz

${BIN_DIR}/trzsz: $(wildcard ./cmd/trzsz/*.go ./trzsz/*.go)
	go build -o $@ ./cmd/trzsz

clean:
	-rm -f ${BIN_DIR}/*

install: all
	mkdir -p ${DESTDIR}${BIN_DST}
	cp ${BIN_DIR}/trz ${DESTDIR}${BIN_DST}/
	cp ${BIN_DIR}/tsz ${DESTDIR}${BIN_DST}/
	cp ${BIN_DIR}/trzsz ${DESTDIR}${BIN_DST}/

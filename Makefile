BIN_DIR := ./bin
BIN_DST := /usr/bin

ifdef GOOS
	ifeq (${GOOS}, windows)
		WIN_TARGET := True
	endif
else
	ifeq (${OS}, Windows_NT)
		WIN_TARGET := True
	endif
endif

ifdef WIN_TARGET
	TRZ := trz.exe
	TSZ := tsz.exe
	TRZSZ := trzsz.exe
else
	TRZ := trz
	TSZ := tsz
	TRZSZ := trzsz
endif

.PHONY: all clean install

all: ${BIN_DIR}/${TRZ} ${BIN_DIR}/${TSZ} ${BIN_DIR}/${TRZSZ}

${BIN_DIR}/${TRZ}: $(wildcard ./cmd/trz/*.go ./trzsz/*.go)
	go build -o ${BIN_DIR}/ ./cmd/trz

${BIN_DIR}/${TSZ}: $(wildcard ./cmd/tsz/*.go ./trzsz/*.go)
	go build -o ${BIN_DIR}/ ./cmd/tsz

${BIN_DIR}/${TRZSZ}: $(wildcard ./cmd/trzsz/*.go ./trzsz/*.go)
	go build -o ${BIN_DIR}/ ./cmd/trzsz

clean:
	-rm -f ${BIN_DIR}/${TRZ} ${BIN_DIR}/${TSZ} ${BIN_DIR}/${TRZSZ}

install: all
	mkdir -p ${DESTDIR}${BIN_DST}
	cp ${BIN_DIR}/trz ${DESTDIR}${BIN_DST}/
	cp ${BIN_DIR}/tsz ${DESTDIR}${BIN_DST}/
	cp ${BIN_DIR}/trzsz ${DESTDIR}${BIN_DST}/

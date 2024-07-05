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

GO_TEST := ${shell basename `which gotest 2>/dev/null` 2>/dev/null || echo go test}

.PHONY: all clean test install

all: ${BIN_DIR}/${TRZ} ${BIN_DIR}/${TSZ} ${BIN_DIR}/${TRZSZ}

${BIN_DIR}/${TRZ}: $(wildcard ./cmd/trz/*.go ./trzsz/*.go) go.mod go.sum
	go build -o ${BIN_DIR}/ ./cmd/trz

${BIN_DIR}/${TSZ}: $(wildcard ./cmd/tsz/*.go ./trzsz/*.go) go.mod go.sum
	go build -o ${BIN_DIR}/ ./cmd/tsz

${BIN_DIR}/${TRZSZ}: $(wildcard ./cmd/trzsz/*.go ./trzsz/*.go) go.mod go.sum
	go build -o ${BIN_DIR}/ ./cmd/trzsz

clean:
	-rm -f ${BIN_DIR}/trz ${BIN_DIR}/tsz ${BIN_DIR}/trzsz ${BIN_DIR}/trz.exe ${BIN_DIR}/tsz.exe ${BIN_DIR}/trzsz.exe

test:
	${GO_TEST} -v -count=1 ./trzsz

install: all
	mkdir -p ${DESTDIR}${BIN_DST}
	cp ${BIN_DIR}/trz ${DESTDIR}${BIN_DST}/
	cp ${BIN_DIR}/tsz ${DESTDIR}${BIN_DST}/
	cp ${BIN_DIR}/trzsz ${DESTDIR}${BIN_DST}/

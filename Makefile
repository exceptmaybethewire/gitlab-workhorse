PREFIX=/usr/local
VERSION=$(shell git describe)-$(shell date -u +%Y%m%d.%H%M%S)
BUILD_DIR = $(shell pwd)
export GOPATH=${BUILD_DIR}/_build
export GO15VENDOREXPERIMENT=1
GOBUILD=go build -ldflags "-X main.Version=${VERSION}"
PKG=gitlab.com/gitlab-org/gitlab-workhorse

all: clean-build gitlab-zip-cat gitlab-zip-metadata gitlab-workhorse

gitlab-zip-cat:	${BUILD_DIR}/_build $(shell find cmd/gitlab-zip-cat/ -name '*.go')
	${GOBUILD} -o ${BUILD_DIR}/$@ ${PKG}/cmd/$@

gitlab-zip-metadata:	${BUILD_DIR}/_build $(shell find cmd/gitlab-zip-metadata/ -name '*.go')
	${GOBUILD} -o ${BUILD_DIR}/$@ ${PKG}/cmd/$@

gitlab-workhorse: ${BUILD_DIR}/_build $(shell find . -name '*.go' | grep -v '^\./_')
	${GOBUILD} -o ${BUILD_DIR}/$@ ${PKG}

install: gitlab-workhorse gitlab-zip-cat gitlab-zip-metadata
	mkdir -p $(DESTDIR)${PREFIX}/bin/
	cd ${BUILD_DIR} && install gitlab-workhorse gitlab-zip-cat gitlab-zip-metadata ${DESTDIR}${PREFIX}/bin/

${BUILD_DIR}/_build:
	mkdir -p $@/src/${PKG}
	tar -cf - --exclude _build --exclude .git . | (cd $@/src/${PKG} && tar -xf -)
	touch $@

.PHONY: deps
deps:
	go get -d -u github.com/stretchr/testify
	go get -d -u github.com/rafaeljusto/redigomock

.PHONY: test
test:	clean-build clean-workhorse all deps
	go fmt ${PKG}/... | awk '{ print } END { if (NR > 0) { print "Please run go fmt"; exit 1 } }'
	go test ${PKG}/...
	@echo SUCCESS

coverage:
	go test -cover -coverprofile=test.coverage
	go tool cover -html=test.coverage -o coverage.html
	rm -f test.coverage

fmt:
	go fmt ./...

.PHONY: clean
clean:	clean-workhorse clean-build
	rm -rf testdata/data testdata/scratch

.PHONY:	clean-workhorse
clean-workhorse:
	cd ${BUILD_DIR} && rm -f gitlab-workhorse gitlab-zip-cat gitlab-zip-metadata

release:
	sh _support/release.sh

.PHONY:	clean-build
clean-build:
	rm -rf ${BUILD_DIR}/_build

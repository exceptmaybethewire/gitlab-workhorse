PREFIX=/usr/local
PKG := gitlab.com/gitlab-org/gitlab-workhorse
BUILD_DIR := $(CURDIR)
TARGET_DIR := $(BUILD_DIR)/_build
TARGET_SETUP := $(TARGET_DIR)/.ok
BIN_BUILD_DIR := $(TARGET_DIR)/bin
PKG_BUILD_DIR := $(TARGET_DIR)/src/$(PKG)
COVERAGE_DIR := $(TARGET_DIR)/cover
VERSION := $(shell git describe)-$(shell date -u +%Y%m%d.%H%M%S)
GOBUILD := go build -ldflags "-X main.Version=$(VERSION)"
EXE_ALL := gitlab-zip-cat gitlab-zip-metadata gitlab-workhorse

# Some users may have these variables set in their environment, but doing so could break
# their build process, so unset then
unexport GOROOT
unexport GOBIN

export GOPATH := $(TARGET_DIR)
export PATH := $(GOPATH)/bin:$(PATH)

# Returns a list of all non-vendored (local packages)
LOCAL_PACKAGES = $(shell cd "$(PKG_BUILD_DIR)" && GOPATH=$(GOPATH) go list ./... | grep -v -e '^$(PKG)/vendor/' -e '^$(PKG)/ruby/')

.NOTPARALLEL:

.PHONY:	all
all:	clean-build $(EXE_ALL)

$(TARGET_SETUP):
	@echo "### Setting up $(TARGET_SETUP)"
	rm -rf $(TARGET_DIR)
	mkdir -p "$(dir $(PKG_BUILD_DIR))"
	ln -sf ../../../.. "$(PKG_BUILD_DIR)"
	mkdir -p "$(BIN_BUILD_DIR)"
	touch "$(TARGET_SETUP)"

gitlab-zip-cat:	$(TARGET_SETUP) $(shell find cmd/gitlab-zip-cat/ -name '*.go')
	$(GOBUILD) -o $(BUILD_DIR)/$@ $(PKG)/cmd/$@

gitlab-zip-metadata:	$(TARGET_SETUP) $(shell find cmd/gitlab-zip-metadata/ -name '*.go')
	$(GOBUILD) -o $(BUILD_DIR)/$@ $(PKG)/cmd/$@

gitlab-workhorse:	$(TARGET_SETUP) $(shell find . -name '*.go' | grep -v '^\./_')
	$(GOBUILD) -o $(BUILD_DIR)/$@ $(PKG)

.PHONY:	install
install:	gitlab-workhorse gitlab-zip-cat gitlab-zip-metadata
	@echo "### install"
	mkdir -p $(DESTDIR)$(PREFIX)/bin/
	cd $(BUILD_DIR) && install gitlab-workhorse gitlab-zip-cat gitlab-zip-metadata $(DESTDIR)$(PREFIX)/bin/

.PHONY:	test
test:	$(TARGET_SETUP) govendor prepare-tests
	@echo "### verifying formatting with go fmt"
	@go fmt $(LOCAL_PACKAGES) | awk '{ print } END { if (NR > 0) { print "Please run go fmt"; exit 1 } }'

	_support/detect-context.sh

	@echo "### running govendor sync"
	cd $(PKG_BUILD_DIR) && govendor sync

	@echo "### running tests"
	@go test $(LOCAL_PACKAGES)
	@echo SUCCESS

.PHONY:	govendor
govendor: $(TARGET_SETUP)
	@command -v govendor || go get github.com/kardianos/govendor

.PHONY:	coverage
coverage:	$(TARGET_SETUP) prepare-tests
	@echo "### coverage"
	@go test -cover -coverprofile=test.coverage $(LOCAL_PACKAGES)
	go tool cover -html=test.coverage -o coverage.html
	rm -f test.coverage

.PHONY:	fmt
fmt:
	@echo "### fmt"
	@go fmt $(LOCAL_PACKAGES)

.PHONY:	clean
clean:	clean-workhorse clean-build
	@echo "### clean"
	rm -rf testdata/data testdata/scratch

.PHONY:	clean-workhorse
clean-workhorse:
	@echo "### clean-workhorse"
	rm -f $(EXE_ALL)

.PHONY:	release
release:
	@echo "### release"
	sh _support/release.sh

.PHONY:	clean-build
clean-build:
	@echo "### clean-build"
	rm -rf $(TARGET_DIR)

.PHONY:	prepare-tests
prepare-tests:	testdata/data/group/test.git $(EXE_ALL)

testdata/data/group/test.git:
	git clone --quiet --bare https://gitlab.com/gitlab-org/gitlab-test.git $@

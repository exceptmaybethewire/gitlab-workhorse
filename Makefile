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

unexport GOROOT
unexport GOBIN
export GOPATH := $(TARGET_DIR)
export PATH := $(GOPATH)/bin:$(PATH)

# Returns a list of all non-vendored (local packages)
LOCAL_PACKAGES = $(shell cd "$(PKG_BUILD_DIR)" && GOPATH=$(GOPATH) go list ./... | grep -v '^$(PKG)/vendor/' | grep -v '^$(PKG)/ruby/')
LOCAL_GO_FILES = $(shell find -L $(PKG_BUILD_DIR)  -name "*.go" -not -path "$(PKG_BUILD_DIR)/vendor/*" -not -path "$(PKG_BUILD_DIR)/_build/*")

.NOTPARALLEL:

.PHONY:	all
all:	clean-build $(EXE_ALL)

$(TARGET_SETUP):
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
	mkdir -p $(DESTDIR)$(PREFIX)/bin/
	cd $(BUILD_DIR) && install gitlab-workhorse gitlab-zip-cat gitlab-zip-metadata $(DESTDIR)$(PREFIX)/bin/

.PHONY:	test
test: $(TARGET_SETUP) prepare-tests
	@go test $(LOCAL_PACKAGES)
	@echo SUCCESS

.PHONY:	coverage
coverage:	$(TARGET_SETUP) prepare-tests
	go test -cover -coverprofile=test.coverage $(LOCAL_PACKAGES)
	go tool cover -html=test.coverage -o coverage.html
	rm -f test.coverage

.PHONY:	fmt
fmt:
	go fmt $(LOCAL_PACKAGES)

.PHONY:	clean
clean:	clean-workhorse clean-build
	rm -rf testdata/data testdata/scratch

.PHONY:	clean-workhorse
clean-workhorse:
	rm -f $(EXE_ALL)

.PHONY:	release
release:
	sh _support/release.sh

.PHONY:	clean-build
clean-build:
	rm -rf $(TARGET_DIR)

.PHONY:	prepare-tests
prepare-tests:	testdata/data/group/test.git $(EXE_ALL)

testdata/data/group/test.git:
	git clone --quiet --bare https://gitlab.com/gitlab-org/gitlab-test.git $@

.PHONY: verify
verify: lint vet detect-context check-formatting megacheck govendor-status

.PHONY: lint
lint: $(TARGET_SETUP)
	@command -v golint || go get -v golang.org/x/lint/golint
	# Many uncommented exports means we need to hack this a little...
	@LINT=$$(golint $(LOCAL_PACKAGES)|grep -Ev 'should have|should be|use ALL_CAPS in Go names'); test -z "$$LINT" || (echo "$$LINT" && exit 1)

.PHONY: vet
vet: $(TARGET_SETUP)
	@go vet $(LOCAL_PACKAGES)

.PHONY: detect-context
detect-context: $(TARGET_SETUP)
	_support/detect-context.sh

.PHONY: check-formatting
check-formatting: $(TARGET_SETUP) install-goimports
	@test -z "$$(goimports -e -l $(LOCAL_GO_FILES))" || (echo >&2 "Formatting or imports need fixing: 'make fmt'" && goimports -e -l $(LOCAL_GO_FILES) && false)

.PHONY: megacheck
megacheck: $(TARGET_SETUP)
	@command -v megacheck || go get -v honnef.co/go/tools/cmd/megacheck
	@megacheck -unused.exit-non-zero $(LOCAL_PACKAGES)

.PHONY: govendor-status
govendor-status: $(TARGET_SETUP)
	@command -v govendor || go get github.com/kardianos/govendor
	cd $(PKG_BUILD_DIR) && govendor sync

.PHONY: format
format: $(TARGET_SETUP) install-goimports
    # In addition to fixing imports, goimports also formats your code in the same style as gofmt
	# so it can be used as a replacement.
	@goimports -w -l $(LOCAL_GO_FILES)

.PHONY:	goimports
install-goimports:	$(TARGET_SETUP)
	@command -v goimports || go get -v golang.org/x/tools/cmd/goimports

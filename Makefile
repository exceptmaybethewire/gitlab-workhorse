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
LOCAL_GO_FILES = $(shell find -L $(PKG_BUILD_DIR)  -name "*.go" -not -path "$(PKG_BUILD_DIR)/vendor/*" -not -path "$(PKG_BUILD_DIR)/_build/*")

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
test: $(TARGET_SETUP) prepare-tests
	@echo "### running tests"
	@go test $(LOCAL_PACKAGES)
	@echo SUCCESS

.PHONY:	coverage
coverage:	$(TARGET_SETUP) prepare-tests
	@echo "### coverage"
	@go test -cover -coverprofile=test.coverage $(LOCAL_PACKAGES)
	go tool cover -html=test.coverage -o coverage.html
	rm -f test.coverage

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
prepare-tests:	govendor-sync testdata/data/group/test.git $(EXE_ALL)

testdata/data/group/test.git:
	git clone --quiet --bare https://gitlab.com/gitlab-org/gitlab-test.git $@

.PHONY: verify
verify: lint vet detect-context check-formatting megacheck

.PHONY: lint
lint: $(TARGET_SETUP) govendor-sync
	@echo "### golint"
	@command -v golint || go get -v golang.org/x/lint/golint
	# Many uncommented exports means we need to hack this a little...
	@LINT=$$(golint $(LOCAL_PACKAGES)|grep -Ev 'should have|should be|use ALL_CAPS in Go names'); test -z "$$LINT" || (echo "$$LINT" && exit 1)

.PHONY: vet
vet: $(TARGET_SETUP) govendor-sync
	@echo "### go vet"
	@go vet $(LOCAL_PACKAGES)

.PHONY: detect-context
detect-context: $(TARGET_SETUP)
	_support/detect-context.sh

.PHONY: check-formatting
check-formatting: $(TARGET_SETUP) install-goimports
	@echo "### go vet"
	@test -z "$$(goimports -e -l $(LOCAL_GO_FILES))" || (echo >&2 "Formatting or imports need fixing: 'make fmt'" && goimports -e -l $(LOCAL_GO_FILES) && false)

.PHONY: megacheck
megacheck: $(TARGET_SETUP) govendor-sync
	@echo "### megacheck"
	@command -v megacheck || go get -v honnef.co/go/tools/cmd/megacheck
	@megacheck -unused.exit-non-zero $(LOCAL_PACKAGES)

# Some vendor components, used for testing are GPL, so we don't distribute them
# and need to go a sync before using them
.PHONY: govendor-sync
govendor-sync: $(TARGET_SETUP)
	@command -v govendor || go get github.com/kardianos/govendor
	@echo "### govendor sync"
	@cd $(PKG_BUILD_DIR) && govendor sync

# In addition to fixing imports, goimports also formats your code in the same style as gofmt
# so it can be used as a replacement.
.PHONY: fmt
fmt: $(TARGET_SETUP) install-goimports
	@echo "### goimports"
	goimports -w -l $(LOCAL_GO_FILES)

.PHONY:	goimports
install-goimports:	$(TARGET_SETUP)
	@command -v goimports || go get -v golang.org/x/tools/cmd/goimports

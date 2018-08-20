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

MINIMUM_SUPPORTED_GO_VERSION := 1.8

# Some users may have these variables set in their environment, but doing so could break
# their build process, so unset then
unexport GOROOT
unexport GOBIN

export GOPATH := $(TARGET_DIR)
export PATH := $(GOPATH)/bin:$(PATH)

# Returns a list of all non-vendored (local packages)
LOCAL_PACKAGES = $(shell cd "$(PKG_BUILD_DIR)" && GOPATH=$(GOPATH) go list ./... | grep -v -e '^$(PKG)/vendor/' -e '^$(PKG)/ruby/')
LOCAL_GO_FILES = $(shell find -L $(PKG_BUILD_DIR)  -name "*.go" -not -path "$(PKG_BUILD_DIR)/vendor/*" -not -path "$(PKG_BUILD_DIR)/_build/*")

define message
	@echo "### $(1)"
endef

.NOTPARALLEL:

.PHONY:	all
all:	clean-build $(EXE_ALL)

$(TARGET_SETUP):
	$(call message,"Setting up target directory")
	rm -rf $(TARGET_DIR)
	mkdir -p "$(dir $(PKG_BUILD_DIR))"
	ln -sf ../../../.. "$(PKG_BUILD_DIR)"
	mkdir -p "$(BIN_BUILD_DIR)"
	touch "$(TARGET_SETUP)"

gitlab-zip-cat:	$(TARGET_SETUP) $(shell find cmd/gitlab-zip-cat/ -name '*.go')
	$(call message,Building $@)
	$(GOBUILD) -o $(BUILD_DIR)/$@ $(PKG)/cmd/$@

gitlab-zip-metadata:	$(TARGET_SETUP) $(shell find cmd/gitlab-zip-metadata/ -name '*.go')
	$(call message,Building $@)
	$(GOBUILD) -o $(BUILD_DIR)/$@ $(PKG)/cmd/$@

gitlab-workhorse:	$(TARGET_SETUP) $(shell find . -name '*.go' | grep -v '^\./_')
	$(call message,Building $@)
	$(GOBUILD) -o $(BUILD_DIR)/$@ $(PKG)

.PHONY:	install
install:	gitlab-workhorse gitlab-zip-cat gitlab-zip-metadata
	$(call message,$@)
	mkdir -p $(DESTDIR)$(PREFIX)/bin/
	cd $(BUILD_DIR) && install gitlab-workhorse gitlab-zip-cat gitlab-zip-metadata $(DESTDIR)$(PREFIX)/bin/

.PHONY:	test
test: $(TARGET_SETUP) prepare-tests
	$(call message,$@)
	@go test $(LOCAL_PACKAGES)
	@echo SUCCESS

.PHONY:	coverage
coverage:	$(TARGET_SETUP) prepare-tests
	$(call message,$@)
	@go test -cover -coverprofile=test.coverage $(LOCAL_PACKAGES)
	go tool cover -html=test.coverage -o coverage.html
	rm -f test.coverage

.PHONY:	clean
clean:	clean-workhorse clean-build
	$(call message,$@)
	rm -rf testdata/data testdata/scratch

.PHONY:	clean-workhorse
clean-workhorse:
	$(call message,$@)
	rm -f $(EXE_ALL)

.PHONY:	release
release:
	$(call message,$@)
	sh _support/release.sh

.PHONY:	clean-build
clean-build:
	$(call message,$@)
	rm -rf $(TARGET_DIR)

.PHONY:	prepare-tests
prepare-tests:	govendor-sync testdata/data/group/test.git $(EXE_ALL)

testdata/data/group/test.git:
	$(call message,$@)
	git clone --quiet --bare https://gitlab.com/gitlab-org/gitlab-test.git $@

.PHONY: verify
verify: lint vet detect-context check-formatting megacheck

.PHONY: lint
lint: $(TARGET_SETUP) govendor-sync
	$(call message,Verify: $@)
	@command -v golint || go get -v golang.org/x/lint/golint
	@_support/lint.sh $(LOCAL_PACKAGES)

.PHONY: vet
vet: $(TARGET_SETUP) govendor-sync
	$(call message,Verify: $@)
	@go vet $(LOCAL_PACKAGES)

.PHONY: detect-context
detect-context: $(TARGET_SETUP)
	$(call message,Verify: $@)
	_support/detect-context.sh

.PHONY: check-formatting
check-formatting: $(TARGET_SETUP) install-goimports
	$(call message,Verify: $@)
	@_support/validate-formatting.sh $(LOCAL_GO_FILES)

# Megacheck will tailor some responses given a minimum Go version, so pass that through the CLI
# Additionally, megacheck will not return failure exit codes unless explicitely told to via the
# `-simple.exit-non-zero` `-unused.exit-non-zero` and `-staticcheck.exit-non-zero` flags
.PHONY: megacheck
megacheck: $(TARGET_SETUP) govendor-sync
	$(call message,Verify: $@)
	@command -v megacheck || go get -v honnef.co/go/tools/cmd/megacheck
	@megacheck -go $(MINIMUM_SUPPORTED_GO_VERSION) -simple.exit-non-zero -unused.exit-non-zero -staticcheck.exit-non-zero $(LOCAL_PACKAGES)

# Some vendor components, used for testing are GPL, so we don't distribute them
# and need to go a sync before using them
.PHONY: govendor-sync
govendor-sync: $(TARGET_SETUP)
	$(call message,$@)
	@command -v govendor || go get github.com/kardianos/govendor
	@cd $(PKG_BUILD_DIR) && govendor sync

# In addition to fixing imports, goimports also formats your code in the same style as gofmt
# so it can be used as a replacement.
.PHONY: fmt
fmt: $(TARGET_SETUP) install-goimports
	$(call message,$@)
	@goimports -w -l $(LOCAL_GO_FILES)

.PHONY:	goimports
install-goimports:	$(TARGET_SETUP)
	$(call message,$@)
	@command -v goimports || go get -v golang.org/x/tools/cmd/goimports

include ./Makefile.Common

# This is the code that we want to run lint, etc.
ALL_SRC := $(shell find . -name '*.go' \
							-not -path './internal/tools/*' \
							-not -path '*/third_party/*' \
							-not -path './pdata/internal/data/protogen/*' \
							-not -path './service/internal/zpages/tmplgen/*' \
							-type f | sort)

# All source code and documents. Used in spell check.
ALL_DOC := $(shell find . \( -name "*.md" -o -name "*.yaml" \) \
                                -type f | sort)

# All Markdown files. Used in markdownlint.
ALL_MD := $(shell find . -name "*.md" -type f | sort)

# ALL_MODULES includes ./* dirs (excludes . dir)
ALL_MODULES := $(shell find . -mindepth 2 \
				-type f \
				-name "go.mod" \
				-not -path "./internal/tools/*" \
				-exec dirname {} \; | sort )

CMD?=

RUN_CONFIG?=examples/local/otel-config.yaml
CONTRIB_PATH=$(CURDIR)/../opentelemetry-collector-contrib
COMP_REL_PATH=cmd/otelcorecol/components.go
MOD_NAME=go.opentelemetry.io/collector

# Function to execute a command. Note the empty line before endef to make sure each command
# gets executed separately instead of concatenated with previous one.
# Accepts command to execute as first parameter.
define exec-command
$(1)

endef

.DEFAULT_GOAL := all

.PHONY: all
all: checklicense checkdoc misspell markdownlint goimpi goporto multimod-verify golint gotest

all-modules:
	@echo $(ALL_MODULES) | tr ' ' '\n' | sort

.PHONY: gomoddownload
gomoddownload:
	@$(MAKE) for-all-target TARGET="moddownload"

.PHONY: gotest
gotest:
	@$(MAKE) for-all-target TARGET="test"

.PHONY: gobenchmark
gobenchmark:
	@$(MAKE) for-all-target TARGET="benchmark"
	cat `find . -name benchmark.txt` > benchmarks.txt

.PHONY: gotest-with-cover
gotest-with-cover:
	@$(MAKE) for-all-target TARGET="test-with-cover"
	$(GOCMD) tool covdata textfmt -i=./coverage/unit -o ./coverage.txt

.PHONY: gotest-with-junit
gotest-with-junit:
	@$(MAKE) for-all-target TARGET="test-with-junit"

.PHONY: goporto
goporto:
	$(GO_TOOL) porto -w --include-internal --skip-dirs "^cmd/mdatagen/third_party$$" ./

.PHONY: for-all
for-all:
	@echo "running $${CMD} in root"
	@$${CMD}
	@set -e; for dir in $(GOMODULES); do \
	  (cd "$${dir}" && \
	  	echo "running $${CMD} in $${dir}" && \
	 	$${CMD} ); \
	done

.PHONY: golint
golint:
	@$(MAKE) for-all-target TARGET="lint"

.PHONY: gomodernize
gomodernize:
	@$(MAKE) for-all-target TARGET="modernize"

.PHONY: goimpi
goimpi:
	@$(MAKE) for-all-target TARGET="impi"

.PHONY: gofmt
gofmt:
	@$(MAKE) for-all-target TARGET="fmt"

.PHONY: gotidy
gotidy:
	@$(MAKE) for-all-target TARGET="tidy"

.PHONY: gogenerate
gogenerate:
	cd cmd/mdatagen && $(GOCMD) install .
	@$(MAKE) for-all-target TARGET="generate"
	$(MAKE) fmt

.PHONY: govulncheck
govulncheck:
	@$(MAKE) for-all-target TARGET="vulncheck"

.PHONY: addlicense
addlicense:
	@ADDLICENSEOUT=`$(GO_TOOL) addlicense -s=only -y "" -c "The OpenTelemetry Authors" $(ALL_SRC) 2>&1`; \
		if [ "$$ADDLICENSEOUT" ]; then \
			echo "$(ADDLICENSE) FAILED => add License errors:\n"; \
			echo "$$ADDLICENSEOUT\n"; \
			exit 1; \
		else \
			echo "Add License finished successfully"; \
		fi

.PHONY: checklicense
checklicense:
	@licRes=$$(for f in $$(find . -type f \( -iname '*.go' -o -iname '*.sh' \) ! -path '**/third_party/*') ; do \
	           awk '/Copyright The OpenTelemetry Authors|generated|GENERATED/ && NR<=3 { found=1; next } END { if (!found) print FILENAME }' $$f; \
			   awk '/SPDX-License-Identifier: Apache-2.0|generated|GENERATED/ && NR<=4 { found=1; next } END { if (!found) print FILENAME }' $$f; \
	   done); \
	   if [ -n "$${licRes}" ]; then \
	           echo "license header checking failed:"; echo "$${licRes}"; \
	           exit 1; \
	   fi

.PHONY: misspell
misspell:
	$(GO_TOOL) misspell -error $(ALL_DOC)

.PHONY: markdownlint
markdownlint:
	npx -y markdownlint-cli@0.48.0 -c .markdownlint.yaml --ignore-path .markdownlintignore -- $(ALL_MD)

.PHONY: misspell-correction
misspell-correction:
	$(GO_TOOL) misspell -w $(ALL_DOC)

.PHONY: run
run: otelcorecol
	./bin/otelcorecol_$(GOOS)_$(GOARCH) --config ${RUN_CONFIG} ${RUN_ARGS}

# Append root module to all modules
GOMODULES = $(ALL_MODULES) $(PWD)

# Define a delegation target for each module
.PHONY: $(GOMODULES)
$(GOMODULES):
	@echo "Running target '$(TARGET)' in module '$@'"
	$(MAKE) -C $@ $(TARGET)

# Triggers each module's delegation target
.PHONY: for-all-target
for-all-target: $(GOMODULES)

.PHONY: check-component
check-component:
ifndef COMPONENT
	$(error COMPONENT variable was not defined)
endif

# Build the Collector executable.
.PHONY: otelcorecol
otelcorecol:
	pushd cmd/otelcorecol && CGO_ENABLED=0 $(GOCMD) build -trimpath -o ../../bin/otelcorecol_$(GOOS)_$(GOARCH) -tags "grpcnotrace" ./... && popd

.PHONY: genotelcorecol
genotelcorecol:
	pushd cmd/builder/ && $(GOCMD) run ./ --skip-compilation --config ../otelcorecol/builder-config.yaml --output-path ../otelcorecol && popd
	$(MAKE) -C cmd/otelcorecol fmt

.PHONY: actionlint
actionlint:
	$(GO_TOOL) actionlint -config-file .github/actionlint.yaml -color .github/workflows/*.yml .github/workflows/*.yaml

.PHONY: ocb
ocb:
	$(MAKE) -C cmd/builder config
	$(MAKE) -C cmd/builder ocb

# Generate structs, functions and tests for pdata package.
genpdata:
	cd internal/cmd/pdatagen && $(GOCMD) run main.go -C $(SRC_ROOT)
	$(MAKE) -C pdata fmt
	cd pdata && $(GO_TOOL) betteralign --generated_files --apply ./... || true

DOCKERCMD ?= docker
DOCKER_PROTOBUF ?= otel/build-protobuf:0.23.0

PROTO_SRC_DIRS := exporter/exporterhelper/internal/queue
PROTO_FILES := $(foreach dir,$(PROTO_SRC_DIRS),$(wildcard $(dir)/*.proto))
PROTOC := $(DOCKERCMD) run --rm -u ${shell id -u} -v${PWD}:${PWD} -w${PWD} ${DOCKER_PROTOBUF} --proto_path=${PWD} --go_out=plugins=grpc,paths=source_relative:.

.PHONY: genproto
genproto:
	@echo "Generating Go code for proto files"
	@echo "Found proto files: $(PROTO_FILES)"
	$(foreach file,$(PROTO_FILES),$(call exec-command,$(PROTOC) $(file)))
	$(MAKE) fmt

# ---------------------------------------------------------------------------
# Performance rig — local benchmarking targets
#
# Targets in this section drive the fanout / pdata clone / pipeline
# benchmarks introduced for the fanout-clone performance work. See
# docs/performance.md for the workflow and perf/README.md for the
# baseline storage convention.
# ---------------------------------------------------------------------------

# Flag presets — overridable from the command line.
PERF_BENCH_FLAGS       ?= -benchmem -benchtime=2s -count=5 -timeout=20m
PERF_BENCH_QUICK_FLAGS ?= -benchmem -benchtime=200ms -count=1 -timeout=10m
PERF_BASELINE_FLAGS    ?= -benchmem -benchtime=5s -count=10 -timeout=30m

PERF_FANOUT_PATTERN   := ^Benchmark(Metrics|Traces|Logs)Fanout$$
PERF_CLONE_PATTERN    := ^BenchmarkCopyTo(Metrics|Traces|Logs)$$
PERF_PIPELINE_PATTERN := ^BenchmarkPipelineFanout(Metrics|Traces|Logs)$$

# Default baseline output directory. Override with BASELINE_DIR=...
BASELINE_DIR ?= perf/baselines/$(shell date -u +%Y-%m-%d)-$(shell git rev-parse --short HEAD 2>/dev/null || echo nogit)

.PHONY: perf-bench-fanout
perf-bench-fanout:
	@cd internal/fanoutconsumer && $(GOCMD) test -run=^$$ -bench='$(PERF_FANOUT_PATTERN)' $(PERF_BENCH_FLAGS) ./...

.PHONY: perf-bench-clone
perf-bench-clone:
	@cd pdata && $(GOCMD) test -run=^$$ -bench='$(PERF_CLONE_PATTERN)' $(PERF_BENCH_FLAGS) ./pmetric/... ./ptrace/... ./plog/...

.PHONY: perf-bench-pipeline
perf-bench-pipeline:
	@cd service && $(GOCMD) test -run=^$$ -bench='$(PERF_PIPELINE_PATTERN)' $(PERF_BENCH_FLAGS) ./internal/graph/...

# perf-bench runs the full local rig (fanout + clone + pipeline) and tees
# the combined output to perf/last-run/bench.txt. Tight enough to iterate
# on a change locally.
.PHONY: perf-bench
perf-bench:
	@mkdir -p perf/last-run
	@( \
	  $(MAKE) -s perf-bench-fanout && \
	  $(MAKE) -s perf-bench-clone && \
	  $(MAKE) -s perf-bench-pipeline \
	) 2>&1 | tee perf/last-run/bench.txt
	@echo "Done. Combined output: perf/last-run/bench.txt"

# perf-bench-quick uses a much shorter benchtime for sub-30s feedback.
# Numbers are noisy — useful for confirming a change compiles and is in
# the right direction, not for shipping deltas.
.PHONY: perf-bench-quick
perf-bench-quick:
	@PERF_BENCH_FLAGS='$(PERF_BENCH_QUICK_FLAGS)' $(MAKE) -s perf-bench

# perf-baseline captures a stable bench output plus metadata into
# perf/baselines/YYYY-MM-DD-<shortsha>/. pprof binaries are NOT captured
# by this target — see docs/performance.md for the steady-state recipe
# via cmd/perftestbed.
.PHONY: perf-baseline
perf-baseline:
	@mkdir -p $(BASELINE_DIR)
	@PERF_BENCH_FLAGS='$(PERF_BASELINE_FLAGS)' $(MAKE) -s perf-bench
	@cp perf/last-run/bench.txt $(BASELINE_DIR)/bench.txt
	@$(MAKE) -s perf-baseline-metadata > $(BASELINE_DIR)/metadata.yaml
	@echo "Baseline captured in $(BASELINE_DIR)"

.PHONY: perf-baseline-metadata
perf-baseline-metadata:
	@echo "captured_at: $$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	@echo "git_sha: $$(git rev-parse HEAD 2>/dev/null || echo nogit)"
	@echo "git_branch: $$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo nogit)"
	@echo "go_version: $$($(GOCMD) version | awk '{print $$3}')"
	@echo "goos: $$($(GOCMD) env GOOS)"
	@echo "goarch: $$($(GOCMD) env GOARCH)"
	@echo "host: $$(hostname -s 2>/dev/null || echo unknown)"
	@echo "flags: \"$(PERF_BASELINE_FLAGS)\""

# perf-compare runs perf-bench at HEAD and compares against a stored
# baseline using benchstat. Install via:
#   $(GOCMD) install golang.org/x/perf/cmd/benchstat@latest
.PHONY: perf-compare
perf-compare:
	@if [ -z "$(BASELINE)" ]; then \
	  echo "Usage: make perf-compare BASELINE=perf/baselines/<dir>"; \
	  exit 1; \
	fi
	@if [ ! -f "$(BASELINE)/bench.txt" ]; then \
	  echo "Missing $(BASELINE)/bench.txt — capture a baseline first with 'make perf-baseline'"; \
	  exit 1; \
	fi
	@$(MAKE) -s perf-bench
	@echo "---"
	@echo "benchstat $(BASELINE)/bench.txt vs perf/last-run/bench.txt"
	@benchstat $(BASELINE)/bench.txt perf/last-run/bench.txt

# perftestbed builds the steady-state profile binary. The pprof endpoint
# is exposed on localhost:6060 by default; see cmd/perftestbed/main.go.
.PHONY: perftestbed
perftestbed:
	@cd cmd/perftestbed && $(GOCMD) build -o ../../bin/perftestbed .
	@echo "Built bin/perftestbed"

ALL_MOD_PATHS := "" $(ALL_MODULES:.%=%)

.PHONY: prepare-contrib
prepare-contrib:
	@echo Setting contrib at $(CONTRIB_PATH) to use this core checkout
	@$(MAKE) -j2 -C $(CONTRIB_PATH) for-all CMD="$(GOCMD) mod edit \
		$(addprefix -replace ,$(join $(ALL_MOD_PATHS:%=go.opentelemetry.io/collector%=),$(ALL_MOD_PATHS:%=$(CURDIR)%)))"
	@$(MAKE) -j2 -C $(CONTRIB_PATH) gotidy

	@$(MAKE) generate-contrib

# Checks that the HEAD of the contrib repo checked out in CONTRIB_PATH compiles
# against the current version of this repo.
.PHONY: check-contrib
check-contrib:
	@echo -e "\nRunning tests"
	@$(MAKE) -C $(CONTRIB_PATH) gotest

	@if [ -z "$(SKIP_RESTORE_CONTRIB)" ]; then \
		$(MAKE) restore-contrib; \
	fi

.PHONY: generate-contrib
generate-contrib:
	@echo -e "\nGenerating files in contrib"
	$(MAKE) -C $(CONTRIB_PATH) generate GROUP=all

# Restores contrib to its original state after running check-contrib.
.PHONY: restore-contrib
restore-contrib:
	@echo -e "\nRestoring contrib at $(CONTRIB_PATH) to its original state"
	@$(MAKE) -C $(CONTRIB_PATH) for-all CMD="$(GOCMD) mod edit \
		$(addprefix -dropreplace ,$(ALL_MOD_PATHS:%=go.opentelemetry.io/collector%))"
	@$(MAKE) -C $(CONTRIB_PATH) for-all CMD="$(GOCMD) mod tidy"

# List of directories where certificates are stored for unit tests.
CERT_DIRS := localhost|""|config/configgrpc/testdata \
             localhost|""|config/confighttp/testdata \
             example1|"-1"|config/configtls/testdata \
             example2|"-2"|config/configtls/testdata
cert-domain = $(firstword $(subst |, ,$1))
cert-suffix = $(word 2,$(subst |, ,$1))
cert-dir = $(word 3,$(subst |, ,$1))

# Generate certificates for unit tests relying on certificates.
.PHONY: certs
certs:
	$(foreach dir, $(CERT_DIRS), $(call exec-command, @internal/buildscripts/gen-certs.sh -o $(call cert-dir,$(dir)) -s $(call cert-suffix,$(dir)) -m $(call cert-domain,$(dir))))

# Generate certificates for unit tests relying on certificates without copying certs to specific test directories.
.PHONY: certs-dryrun
certs-dryrun:
	@internal/buildscripts/gen-certs.sh -d

.PHONY: checkapi
checkapi:
	$(GO_TOOL) checkapi -folder . -config .checkapi.yaml

# Verify existence of READMEs for components specified as default components in the collector.
.PHONY: checkdoc
checkdoc:
	$(GO_TOOL) checkfile --project-path $(CURDIR) --component-rel-path $(COMP_REL_PATH) --module-name $(MOD_NAME) --file-name "README.md"

# Construct new API state snapshots
.PHONY: apidiff-build
apidiff-build:
	@$(foreach pkg,$(ALL_PKGS),$(call exec-command,./internal/buildscripts/gen-apidiff.sh -p $(pkg)))

# If we are running in CI, change input directory
ifeq ($(CI), true)
APICOMPARE_OPTS=$(COMPARE_OPTS)
else
APICOMPARE_OPTS=-d "./internal/data/apidiff"
endif

# Compare API state snapshots
.PHONY: apidiff-compare
apidiff-compare:
	@$(foreach pkg,$(ALL_PKGS),$(call exec-command,./internal/buildscripts/compare-apidiff.sh -p $(pkg)))

.PHONY: multimod-verify
multimod-verify:
	@echo "Validating versions.yaml"
	$(GO_TOOL) multimod verify

MODSET?=stable
.PHONY: multimod-prerelease
multimod-prerelease:
	$(GO_TOOL) multimod prerelease -s=true -b=false -v ./versions.yaml -m ${MODSET}
	$(MAKE) gotidy

COMMIT?=HEAD
REMOTE?=git@github.com:open-telemetry/opentelemetry-collector.git
.PHONY: push-tags
push-tags:
	$(GO_TOOL) multimod verify
	set -e; for tag in `$(GO_TOOL) multimod tag -m ${MODSET} -c ${COMMIT} --print-tags | grep -v "Using" `; do \
		echo "pushing tag $${tag}"; \
		git push ${REMOTE} $${tag}; \
	done;

.PHONY: check-changes
check-changes:
	$(GO_TOOL) multimod diff -p $(PREVIOUS_VERSION) -m $(MODSET)

.PHONY: prepare-release
prepare-release:
ifndef MODSET
	@echo "MODSET not defined"
	@echo "usage: make prepare-release RELEASE_CANDIDATE=<version eg 0.53.0> PREVIOUS_VERSION=<version eg 0.52.0> MODSET=beta"
	exit 1
endif
ifdef PREVIOUS_VERSION
	@echo "Previous version $(PREVIOUS_VERSION)"
else
	@echo "PREVIOUS_VERSION not defined"
	@echo "usage: make prepare-release RELEASE_CANDIDATE=<version eg 0.53.0> PREVIOUS_VERSION=<version eg 0.52.0> MODSET=beta"
	exit 1
endif
ifdef RELEASE_CANDIDATE
	@echo "Preparing ${MODSET} release $(RELEASE_CANDIDATE)"
else
	@echo "RELEASE_CANDIDATE not defined"
	@echo "usage: make prepare-release RELEASE_CANDIDATE=<version eg 0.53.0> PREVIOUS_VERSION=<version eg 0.52.0> MODSET=beta"
	exit 1
endif
	# ensure a clean branch
	git diff -s --exit-code || (echo "local repository not clean"; exit 1)
	# update files with new version
	sed -i.bak 's/$(PREVIOUS_VERSION)/$(RELEASE_CANDIDATE)/g' versions.yaml
	sed -i.bak 's/$(PREVIOUS_VERSION)/$(RELEASE_CANDIDATE)/g' ./cmd/builder/internal/builder/config.go
	sed -i.bak 's/$(PREVIOUS_VERSION)/$(RELEASE_CANDIDATE)/g' ./cmd/builder/test/core.builder.yaml
	sed -i.bak 's/$(PREVIOUS_VERSION)/$(RELEASE_CANDIDATE)/g' ./cmd/otelcorecol/builder-config.yaml
	sed -i.bak 's/$(PREVIOUS_VERSION)/$(RELEASE_CANDIDATE)/g' examples/k8s/otel-config.yaml
	find . -name "*.bak" -type f -delete
	# commit changes before running multimod
	git add .
	git commit -m "prepare release $(RELEASE_CANDIDATE)"
	$(MAKE) multimod-prerelease
	# regenerate files
	$(MAKE) -C cmd/builder config
	$(MAKE) genotelcorecol
	git add .
	git commit -m "add multimod changes $(RELEASE_CANDIDATE)" || (echo "no multimod changes to commit")

.PHONY: clean
clean:
	test -d bin && $(RM) bin/*

.PHONY: checklinks
checklinks:
	command -v $(DOCKERCMD) >/dev/null 2>&1 || { echo >&2 "$(DOCKERCMD) not installed. Install before continuing"; exit 1; }
	$(DOCKERCMD) run -w /home/repo --rm \
		--mount 'type=bind,source='$(PWD)',target=/home/repo' \
		lycheeverse/lychee:0.23 \
		--config .github/lychee.toml \
		--root-dir /home/repo \
		-v \
		--no-progress './**/*.md'

# error message "failed to sync logger:  sync /dev/stderr: inappropriate ioctl for device"
# is a known issue but does not affect function.
.PHONY: crosslink
crosslink:
	@echo "Executing crosslink"
	$(GO_TOOL) crosslink --root=$(shell pwd) --prune

FILENAME?=$(shell git branch --show-current)
.PHONY: chlog-new
chlog-new:
	$(GO_TOOL) chloggen new --config $(CHLOGGEN_CONFIG) --filename $(FILENAME)

.PHONY: chlog-validate
chlog-validate:
	$(GO_TOOL) chloggen validate --config $(CHLOGGEN_CONFIG)

.PHONY: chlog-preview
chlog-preview:
	$(GO_TOOL) chloggen update --config $(CHLOGGEN_CONFIG) --dry

.PHONY: chlog-update
chlog-update:
	$(GO_TOOL) chloggen update --config $(CHLOGGEN_CONFIG) --version $(VERSION)

.PHONY: builder-integration-test
builder-integration-test:
	cd ./cmd/builder && ./test/test.sh

.PHONY: mdatagen-test
mdatagen-test:
	cd cmd/mdatagen && $(GOCMD) install .
	cd cmd/mdatagen && $(GOCMD) generate ./...
	cd cmd/mdatagen && $(MAKE) fmt
	cd cmd/mdatagen && $(GOCMD) test ./...

GITHUBGEN_ARGS ?= -skipgithub
GITHUBGEN := $(GO_TOOL) githubgen $(GITHUBGEN_ARGS)

.PHONY: generate-gh-issue-templates
generate-gh-issue-templates:
	$(GITHUBGEN) issue-templates

.PHONY: generate-codeowners
generate-codeowners:
	$(GITHUBGEN) --default-codeowner "open-telemetry/collector-approvers" codeowners

.PHONY: gengithub
gengithub: generate-codeowners generate-gh-issue-templates

.PHONY: gendistributions
gendistributions:
	$(GITHUBGEN) distributions

.PHONY: generate-chloggen-components
generate-chloggen-components:
	$(GITHUBGEN) chloggen-components

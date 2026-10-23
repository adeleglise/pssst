RUNTIME ?= podman
COMPOSE ?= $(RUNTIME) compose
IMAGE ?= pssst:dev
PLATFORMS ?= linux/amd64 linux/arm64 darwin/arm64

# The exporter reports this through -version, every log record and
# psp_exporter_build_info. Tag v1.0.0 yields "1.0.0", like the versions
# already deployed; an untagged commit yields its description rather than a
# misleading version number.
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//'),dev)

.PHONY: fmt fmt-check lint vuln test test-race build dist image up down rules-test smoke chart-sync chart-test dashboards

fmt:
	@command -v rg >/dev/null
	gofmt -w $$(rg --files -g '*.go' .)

fmt-check:
	@command -v rg >/dev/null
	test -z "$$(gofmt -l $$(rg --files -g '*.go' .))"

# Analyzers are pinned as go.mod tools, so CI and workstations agree.
lint:
	go vet ./...
	go tool staticcheck ./...

# Needs network access to the Go vulnerability database.
vuln:
	go tool govulncheck ./...

test:
	go test ./...

test-race:
	go test -race ./...

build:
	mkdir -p bin
	go build -ldflags="-X main.version=$(VERSION)" -o bin/pssst ./cmd/psp-exporter
	go build -o bin/fake-psp ./cmd/fake-psp
	go build -o bin/pssst-check ./cmd/pssst-check

# Release binaries: static, stripped, one file per tool and platform, plus
# their checksums. The release workflow publishes exactly this directory.
dist:
	rm -rf dist
	mkdir -p dist
	for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; suffix=$(VERSION)_$${os}_$${arch}; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o dist/pssst_$$suffix ./cmd/psp-exporter || exit 1; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="-s -w" -o dist/pssst-check_$$suffix ./cmd/pssst-check || exit 1; \
	done
	cd dist && shasum -a 256 pssst* > SHA256SUMS

image:
	$(RUNTIME) build --target exporter --build-arg VERSION=$(VERSION) --tag $(IMAGE) .

up:
	$(COMPOSE) up --build --detach

down:
	$(COMPOSE) down --remove-orphans

rules-test:
	promtool check rules deploy/prometheus/pssst.yml
	promtool test rules deploy/prometheus/pssst-rules-test.yml

smoke:
	RUNTIME=$(RUNTIME) ./scripts/smoke.sh

# The chart ships copies of the inventory and the Blackbox modules, because
# Helm cannot read outside the chart. deploy/deploy_test.go fails when they
# drift from their sources; this refreshes them.
chart-sync:
	cp deploy/pssst.psp.yml deploy/helm/pssst/files/pssst.psp.yml
	cp deploy/blackbox/blackbox.yml deploy/helm/pssst/files/blackbox.yml
	cp deploy/prometheus/pssst.yml deploy/helm/pssst/files/pssst.yml

# Needs helm, and kustomize or kubectl: renders the chart and the kustomize
# base and fails on any difference between them.
chart-test:
	helm lint deploy/helm/pssst
	PSSST_REQUIRE_DEPLOY_TOOLS=1 go test -count=1 ./deploy/

# Both dashboards are generated from deploy/grafana/build_dashboard.py; CI
# fails when a committed file differs from what the script produces.
dashboards:
	cd deploy/grafana && python3 build_dashboard.py > pssst-v2.json
	cd deploy/grafana && python3 build_dashboard.py --grafana-com > pssst-grafana-com.json

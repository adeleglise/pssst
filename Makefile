RUNTIME ?= podman
COMPOSE ?= $(RUNTIME) compose
IMAGE ?= pssst:dev

.PHONY: fmt fmt-check lint test test-race build image up down rules-test smoke

fmt:
	@command -v rg >/dev/null
	gofmt -w $$(rg --files -g '*.go')

fmt-check:
	@command -v rg >/dev/null
	test -z "$$(gofmt -l $$(rg --files -g '*.go'))"

lint:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

build:
	mkdir -p bin
	go build -o bin/pssst ./cmd/psp-exporter
	go build -o bin/fake-psp ./cmd/fake-psp
	go build -o bin/pssst-check ./cmd/pssst-check

image:
	$(RUNTIME) build --target exporter --tag $(IMAGE) .

up:
	$(COMPOSE) up --build --detach

down:
	$(COMPOSE) down --remove-orphans

rules-test:
	promtool check rules deploy/prometheus/pssst.yml
	promtool test rules deploy/prometheus/pssst-rules-test.yml

smoke:
	RUNTIME=$(RUNTIME) ./scripts/smoke.sh

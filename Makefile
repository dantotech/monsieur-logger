APP        := monsieur-logger
IMAGE      ?= ghcr.io/dantotech/$(APP)
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG        := github.com/dantotech/$(APP)/internal/version
LDFLAGS    := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
CHART      := deployments/helm/$(APP)

.PHONY: all build run test vet fmt sync-logo docker-build docker-push helm-lint helm-template helm-install clean

all: vet test build

build: sync-logo
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/$(APP)

run: build
	LOG_LEVEL=debug ./bin/$(APP)

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

# The logo lives in assets/; the Swagger UI embeds a copy from web/swagger/.
sync-logo:
	cp assets/$(APP).svg web/swagger/logo.svg

docker-build:
	docker build -f build/package/Dockerfile \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
	  -t $(IMAGE):$(VERSION) .

docker-push:
	docker push $(IMAGE):$(VERSION)

helm-lint:
	helm lint $(CHART)

helm-template:
	helm template $(APP) $(CHART)

helm-install:
	helm upgrade --install $(APP) $(CHART) --namespace $(APP) --create-namespace \
	  --set image.repository=$(IMAGE) --set image.tag=$(VERSION)

clean:
	rm -rf bin dist

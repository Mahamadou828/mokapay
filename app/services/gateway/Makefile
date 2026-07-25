SERVICE := gateway
-include ../../../vars.env
export

IMAGE := $(GATEWAY_IMAGE)
APP   := $(GATEWAY_APP)

.PHONY: build check lint tidy test serve \
        image restart restart-dev logs logs-dev describe describe-deployment

# ==============================================================================
# Build & verify

build:
	go build -o bin/$(SERVICE) .

check:
	go build ./...

lint:
	go vet ./...
	go fmt ./...

tidy:
	cd ../../.. && go mod tidy

test:
	go test ./...

# ==============================================================================
# Run locally

serve:
	go run . run 2>&1 | go run ../../../app/tooling/logfmt -service $(SERVICE)

# ==============================================================================
# Docker / k8s operations

image:
	cd ../../.. && docker build -f app/services/$(SERVICE)/Dockerfile -t "$(IMAGE)" \
		--build-arg BUILD_REF="$(VERSION)" \
		--build-arg BUILD_DATE="$$(date -u +%Y-%m-%dT%H:%M:%SZ)" .

restart:
	cd ../../.. && docker compose up -d --build $(SERVICE)

restart-dev:
	kubectl rollout restart deployment "$(APP)" --namespace="$(NAMESPACE)"

logs:
	cd ../../.. && docker compose logs -f --no-log-prefix $(SERVICE) | go run ./app/tooling/logfmt -service $(SERVICE)

logs-dev:
	kubectl logs -n "$(NAMESPACE)" -l app="$(APP)" --all-containers=true -f --tail=100 --max-log-requests=10 | go run ../../../app/tooling/logfmt -service $(SERVICE)

describe:
	kubectl describe pod --namespace="$(NAMESPACE)" -l app="$(APP)"

describe-deployment:
	kubectl describe deployment --namespace="$(NAMESPACE)" "$(APP)"

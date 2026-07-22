# todo: add command to install dependencies
include vars.env
export

# ==============================================================================
# Define dependencies
GOLANG              := golang:1.26
ALPINE              := alpine:3.23
KIND                := kindest/node:v1.36.1
POSTGRES            := postgres:18.4
GRAFANA             := grafana/grafana:12.4.0
PROMETHEUS          := prom/prometheus:v3.12.0
TEMPO               := grafana/tempo:2.10.0
LOKI                := grafana/loki:3.7.0
PROMTAIL            := grafana/promtail:3.6.0

GO_SERVICES := gateway

.PHONY: docs proto build check-all lint-all test-all tidy start stop

# ==============================================================================
# Service-scoped commands are delegated to each app's Makefile:
#   make gateway/serve   →  make -C app/services/gateway serve
#   make gateway/logs    →  make -C app/services/gateway logs
# One rule per app: a single pattern rule with several %-targets would tell
# make that one recipe invocation updates all of them, so fan-outs like
# check-all would only really run the first service.
gateway/%:
	$(MAKE) -C app/services/gateway $(@F)

# Fan-outs over the Go services
check-all: $(addsuffix /check,$(GO_SERVICES))
lint-all: $(addsuffix /lint,$(GO_SERVICES))
test-all: $(addsuffix /test,$(GO_SERVICES))
tidy:
	go mod tidy

# ==============================================================================
# Protobuf code generation (proto/codebase is committed; run after editing proto/)
proto:
	buf generate

# ==============================================================================
# Local infrastructure (compose)
start:
	docker compose up -d

stop:
	docker compose down

# ==============================================================================
# creating a design doc folder (see .claude/rules/docs.md)
# example: make docs n=title
docs:
	mkdir ./docs/$$(date +%Y%m%d_%H%M)_$(n)

# update submodule dependencies
submodule-update:
	git submodule update

# ==============================================================================
# Building containers
# Image builds live in each app's Makefile as `image`; this fans out.
build: gateway/image

# ==============================================================================
# Starting kind cluster
kind-up:
	kind create cluster --image $(KIND) --name $(KIND_CLUSTER) --wait 300s --config infra/k8s/dev/kind-config.yaml
	docker save $(POSTGRES) | docker exec -i $(KIND_CLUSTER)-control-plane ctr --namespace=k8s.io images import - & \
	docker save $(GRAFANA) | docker exec -i $(KIND_CLUSTER)-control-plane ctr --namespace=k8s.io images import - & \
	docker save $(PROMETHEUS) | docker exec -i $(KIND_CLUSTER)-control-plane ctr --namespace=k8s.io images import - & \
	docker save $(TEMPO) | docker exec -i $(KIND_CLUSTER)-control-plane ctr --namespace=k8s.io images import - & \
	docker save $(LOKI) | docker exec -i $(KIND_CLUSTER)-control-plane ctr --namespace=k8s.io images import - & \
	docker save $(PROMTAIL) | docker exec -i $(KIND_CLUSTER)-control-plane ctr --namespace=k8s.io images import - & \
	wait;

kind-down:
	kind delete cluster --name $(KIND_CLUSTER)

kind-status-all:
	kubectl get nodes -o wide
	kubectl get svc -o wide
	kubectl get pods -o wide --watch --all-namespaces

kind-status:
	watch -n 2 kubectl get pods -o wide --all-namespaces

# ==============================================================================
# Managing kind cluster
kind-load-images:
	kind load docker-image $(GATEWAY_IMAGE) --name $(KIND_CLUSTER)

kind-apply:
	kustomize build infra/k8s/dev/gateway | kubectl apply -f -
	kubectl wait pods --namespace=$(NAMESPACE) --selector app=$(GATEWAY_APP) --timeout=120s --for=condition=Ready

kind-clean:
	kustomize build infra/k8s/dev/gateway | kubectl delete -f -

kind-restart: gateway/restart-dev

kind-update: build kind-load-images kind-restart

kind-update-apply: build kind-load-images kind-apply

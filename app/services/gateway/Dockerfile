FROM golang:1.26 AS build-stage
ENV CGO_ENABLED 0
ARG BUILD_REF

WORKDIR /workspace

# Single root module: copy the manifests, download deps (cache-friendly),
# then copy only the source trees this service needs (add the tree of any
# service whose client package the gateway imports).
COPY go.mod go.sum ./
RUN go mod download

COPY core/ ./core/
COPY proto/ ./proto/
COPY app/services/gateway/ ./app/services/gateway/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X mokapay.app/app/services/gateway/internal.build=${BUILD_REF}" -o /gateway ./app/services/gateway

FROM alpine:3.19 AS build-release-stage
ARG BUILD_DATE
ARG BUILD_REF

RUN addgroup -g 1000 -S gateway && adduser -u 1000 -h /gateway -H -G gateway -S gateway

COPY --from=build-stage /gateway /gateway

EXPOSE 8080

USER gateway:gateway

ENTRYPOINT ["/gateway", "run"]

LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.title="gateway" \
      org.opencontainers.image.revision="${BUILD_REF}"

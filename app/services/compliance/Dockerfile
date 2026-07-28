FROM golang:1.26 AS build-stage
ENV CGO_ENABLED 0
ARG BUILD_REF

WORKDIR /workspace

# Single root module: copy the manifests, download deps (cache-friendly),
# then copy only the source trees this service needs.
COPY go.mod go.sum ./
RUN go mod download

COPY core/ ./core/
COPY proto/ ./proto/
COPY app/services/compliance/ ./app/services/compliance/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X mokapay.app/app/services/compliance/internal.build=${BUILD_REF}" -o /compliance ./app/services/compliance

FROM alpine:3.19 AS build-release-stage
ARG BUILD_DATE
ARG BUILD_REF

RUN addgroup -g 1000 -S compliance && adduser -u 1000 -h /compliance -H -G compliance -S compliance

COPY --from=build-stage /compliance /compliance

EXPOSE 50054

USER compliance:compliance

ENTRYPOINT ["/compliance", "run"]

LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.title="compliance" \
      org.opencontainers.image.revision="${BUILD_REF}"

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
COPY app/services/payment/ ./app/services/payment/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X mokapay.app/app/services/payment/internal.build=${BUILD_REF}" -o /payment ./app/services/payment

FROM alpine:3.19 AS build-release-stage
ARG BUILD_DATE
ARG BUILD_REF

RUN addgroup -g 1000 -S payment && adduser -u 1000 -h /payment -H -G payment -S payment

COPY --from=build-stage /payment /payment

EXPOSE 50051

USER payment:payment

ENTRYPOINT ["/payment", "run"]

LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.title="payment" \
      org.opencontainers.image.revision="${BUILD_REF}"

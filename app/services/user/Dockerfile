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
COPY app/services/user/ ./app/services/user/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X mokapay.app/app/services/user/internal.build=${BUILD_REF}" -o /user ./app/services/user

FROM alpine:3.19 AS build-release-stage
ARG BUILD_DATE
ARG BUILD_REF

RUN addgroup -g 1000 -S user && adduser -u 1000 -h /user -H -G user -S user

COPY --from=build-stage /user /user

EXPOSE 50051

USER user:user

ENTRYPOINT ["/user", "run"]

LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.title="user" \
      org.opencontainers.image.revision="${BUILD_REF}"

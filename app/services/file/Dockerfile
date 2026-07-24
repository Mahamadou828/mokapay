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
COPY app/services/file/ ./app/services/file/

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X mokapay.app/app/services/file/internal.build=${BUILD_REF}" -o /file ./app/services/file

FROM alpine:3.19 AS build-release-stage
ARG BUILD_DATE
ARG BUILD_REF

RUN addgroup -g 1000 -S file && adduser -u 1000 -h /file -H -G file -S file

COPY --from=build-stage /file /file

EXPOSE 50051

USER file:file

ENTRYPOINT ["/file", "run"]

LABEL org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.title="file" \
      org.opencontainers.image.revision="${BUILD_REF}"

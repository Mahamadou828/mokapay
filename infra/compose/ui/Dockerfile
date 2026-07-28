# Generic Vite SPA image — one Dockerfile for every frontend, selected
# with UI_DIR (app/ui/mokapay-admin, app/ui/mokapay-mobile). Build from
# the REPO ROOT context so no file lands inside the UI submodules.
#
# VITE_API_URL is baked at build time (Vite inlines import.meta.env).
# The deployment default is "" — relative /v1/... requests, same-origin
# through the edge proxy, no CORS. Point it at an absolute URL only when
# the SPA is served from somewhere the proxy is not.
FROM node:22-alpine AS build-stage
ARG UI_DIR
ARG VITE_API_URL=""

WORKDIR /workspace

# Manifests first: npm ci stays cached until the lockfile changes.
COPY ${UI_DIR}/package.json ${UI_DIR}/package-lock.json ./
RUN npm ci

COPY ${UI_DIR}/ ./

ENV VITE_API_URL=${VITE_API_URL}
RUN npm run build

FROM nginx:1.29-alpine
COPY infra/compose/ui/spa.conf /etc/nginx/conf.d/default.conf
COPY --from=build-stage /workspace/dist /usr/share/nginx/html

EXPOSE 80

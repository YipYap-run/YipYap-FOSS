# Stage 1: Build web assets
FROM node:lts-alpine AS web
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /yipyap ./cmd/yipyap
# Seed an empty data dir so the named volume mounts writable by the runtime
# user (scratch has no shell to mkdir/chown at startup).
RUN mkdir -p /data && touch /data/.keep

# Stage 3: Minimal runtime
FROM scratch
COPY --from=build /yipyap /yipyap
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build --chown=65534:65534 /data /data
USER 65534:65534
ENTRYPOINT ["/yipyap"]

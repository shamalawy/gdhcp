# gdhcp container image — 3-stage build (docs/design/deploy-build.md +
# docs/AMENDMENTS.md A2/A3): Angular frontend -> static Go binary -> alpine
# runtime with the capabilities the DHCP engine needs pre-applied via setcap.
#
# Build from the repo root:
#   docker build -f deploy/docker/Dockerfile -t gdhcp:latest .

# ---- Stage 1: Angular frontend --------------------------------------------
FROM node:20-alpine AS web-builder

WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# ---- Stage 2: Go binary (CGO_ENABLED=0, pure Go per amendment A3) --------
FROM golang:1.26-alpine AS go-builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY server/ ./server/
COPY web/embed.go ./web/embed.go

# web/dist must exist before `go build` because web/embed.go does
# //go:embed all:dist (amendment A4) — copy the Angular build output in
# before compiling, overwriting the committed placeholder index.html.
COPY --from=web-builder /app/web/dist ./web/dist

ARG VERSION=dev
ARG BUILD_TIME=unknown
ARG GIT_COMMIT=unknown

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-X github.com/shamalawy/gdhcp/server/internal/version.Version=${VERSION} \
              -X github.com/shamalawy/gdhcp/server/internal/version.Commit=${GIT_COMMIT} \
              -X github.com/shamalawy/gdhcp/server/internal/version.BuildDate=${BUILD_TIME} \
              -w -s" \
    -o /out/gdhcp ./server/cmd/gdhcp

# ---- Stage 3: runtime -------------------------------------------------
FROM alpine:3.24

# ca-certificates: TLS to Postgres / outbound HTTPS.
# libcap: setcap, so the binary can bind port 67 and manage the VIP without
# running the process as root (see deploy/systemd/gdhcp.service for the
# bare-metal equivalent using AmbientCapabilities).
RUN apk add --no-cache ca-certificates libcap && \
    addgroup -g 1000 gdhcp && \
    adduser -D -u 1000 -G gdhcp -h /var/lib/gdhcp gdhcp

COPY --from=go-builder /out/gdhcp /usr/local/bin/gdhcp

# CAP_NET_BIND_SERVICE: bind UDP/67 as non-root.
# CAP_NET_ADMIN + CAP_NET_RAW: VIP assignment (netlink) and gratuitous ARP
# for HA failover (docs/design/ha-cluster.md). setcap makes these usable by
# the gdhcp user directly; USER below still drops root for everything else.
RUN setcap 'cap_net_admin,cap_net_raw,cap_net_bind_service=+eip' /usr/local/bin/gdhcp

RUN mkdir -p /etc/gdhcp /var/lib/gdhcp /var/log/gdhcp && \
    chown -R gdhcp:gdhcp /etc/gdhcp /var/lib/gdhcp /var/log/gdhcp

# NOTE ON USER: we run as the unprivileged `gdhcp` user relying on the file
# capabilities set above. This works for bare `docker run` and for Compose
# with network_mode: host. It does NOT work if the container's user
# namespace remaps UIDs in a way that strips file capabilities (some
# rootless-Docker / restrictive-runtime setups) — in that case run as root
# (add `user: "0:0"` in compose or drop this USER line) or grant the
# equivalent Linux capabilities via cap_add instead, as
# deploy/docker/docker-compose.yml already does; the two approaches are
# redundant with each other and either alone is sufficient in most engines.
# DHCP itself additionally requires host networking (see docker-compose.yml)
# because broadcast DHCP traffic does not cross the container bridge.
USER gdhcp:gdhcp
WORKDIR /etc/gdhcp

EXPOSE 8080 67/udp 9090

ENTRYPOINT ["/usr/local/bin/gdhcp"]
CMD ["serve", "--config-file", "/etc/gdhcp/config.yaml"]

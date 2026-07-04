# syntax=docker/dockerfile:1.7
# 一体化镜像：前端 build → 嵌入 Go 二进制 → alpine 运行时
#
# 三阶段构建：
#   1) frontend-builder — node + pnpm 跑 `vite build`，产物在 /web/dist
#   2) go-builder       — 把 dist 拷到 web/dist，go build 通过 //go:embed 嵌入二进制
#   3) runtime          — 极小 alpine 镜像只放一个静态二进制
#
# 由于第二阶段需要 frontend 产物，构建 context 必须是 repo 根目录：
#   docker build --build-arg VERSION=0.0.7 -t upstream-ops:0.0.7 .
#   或在 docker-compose 里写 context: .

# ---------- Stage 1: 前端 ----------
# Vite 8 requires Node ^20.19.0 or >=22.12.0. Use Node 22 in CI/Docker builds
# so the frontend stage is unambiguously above the required runtime floor.
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-builder
WORKDIR /web
ENV PNPM_STORE_DIR=/pnpm/store

# pnpm-workspace.yaml 用了 allowBuilds: 这种 pnpm 10.4+ 才支持的字段，
# corepack 默认的 pnpm shim 版本可能太旧，显式 pin 一个已知兼容版本。
RUN corepack enable && corepack prepare pnpm@10.4.0 --activate

# 先拷依赖清单走缓存层
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
# 不用 --frozen-lockfile：lockfile 不严格匹配时只警告不报错；
# 在 CI 里如果发现 lockfile 已经稳定可信，可以改回 --frozen-lockfile 锁定可复现性。
RUN --mount=type=cache,id=upstream-ops-pnpm-store,target=/pnpm/store \
    pnpm install --no-frozen-lockfile --prefer-offline --store-dir=${PNPM_STORE_DIR}

# 再拷源码，build 产物在 /web/dist
COPY frontend/ ./
RUN pnpm build

# ---------- Stage 2: 后端 ----------
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS go-builder
WORKDIR /src
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=0.0.0-dev
ENV GOMODCACHE=/go/pkg/mod \
    GOCACHE=/root/.cache/go-build

# 先 go.mod / go.sum 走缓存
COPY go.mod go.sum ./
RUN --mount=type=cache,id=upstream-ops-go-mod,target=/go/pkg/mod \
    go mod download

# 然后整份源码
COPY . ./

# 关键：把前端 dist 替换掉占位的 web/dist，让 //go:embed 抓到真东西
RUN rm -rf ./web/dist
COPY --from=frontend-builder /web/dist ./web/dist

RUN --mount=type=cache,id=upstream-ops-go-build,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
        -trimpath \
        -ldflags="-s -w -X github.com/ifty-r/upstream-ops/backend/global.VERSION=${VERSION}" \
        -o /out/upstream-ops \
        ./cmd/server

# ---------- Stage 3: 运行时 ----------
FROM alpine:3.20
RUN --mount=type=cache,id=upstream-ops-apk-cache,target=/var/cache/apk \
    apk add --update-cache ca-certificates tzdata wget && \
    mkdir -p /app/data
WORKDIR /app
COPY --from=go-builder /out/upstream-ops /usr/local/bin/upstream-ops
EXPOSE 8418
ENTRYPOINT ["upstream-ops"]

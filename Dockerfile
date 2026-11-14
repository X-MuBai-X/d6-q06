# 题目 06 营销素材 AI 生图平台的构建镜像
# 多阶段构建：编译 → 最小运行镜像
FROM golang:1.27 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockgen ./cmd/mockgen

FROM debian:bookworm-slim
WORKDIR /app
RUN useradd -r -u 10001 studio && mkdir -p /app/data && chown -R studio:studio /app
COPY --from=builder /out/server /app/server
COPY --from=builder /out/mockgen /app/mockgen
USER studio
EXPOSE 8080 8090
ENV STUDIO_ADDR=":8080"
HEALTHCHECK --interval=10s --timeout=3s --retries=5 \
  CMD ["sh", "-c", "exec 3<>/dev/tcp/127.0.0.1/8080"]
ENTRYPOINT ["/app/server"]

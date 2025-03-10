# 构建阶段
FROM golang:1.22 as builder

WORKDIR /app

# 复制源代码和 Makefile
COPY . .

# 安装依赖并构建
# RUN CGO_ENABLED=0 GOOS=linux GOPROXY=https://goproxy.cn,direct GOARCH=amd64 go build -ldflags="-w -s" -o build/index-offset-scheduler ./cmd/main.go
RUN CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -ldflags="-w -s" -o build/index-offset-scheduler ./cmd/main.go
# 最终阶段
FROM alpine:3.18

# 创建必要的目录
RUN mkdir -p /etc/kubernetes

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的主程序
COPY --from=builder /app/build/index-offset-scheduler .

# 设置入口点
ENTRYPOINT ["./index-offset-scheduler"]

# 构建阶段
FROM golang:1.21-alpine AS builder

# 设置工作目录
WORKDIR /app

# 安装必要的构建工具
RUN apk add --no-cache git

# 复制 go.mod 和 go.sum
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 构建应用
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o toupiao .

# 运行阶段
FROM alpine:latest

# 安装 ca-certificates，用于 HTTPS 请求
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# 从构建阶段复制二进制文件
COPY --from=builder /app/toupiao .

# 从构建阶段复制模板文件
COPY --from=builder /app/templates ./templates

# 暴露端口
EXPOSE 8888

# 运行应用
CMD ["./toupiao"]

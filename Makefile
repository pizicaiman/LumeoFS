# LumeoFS Makefile
# 分布式存储系统构建脚本

.PHONY: all build clean test master datanode client cluster-master

# 默认目标
all: build

# 构建所有组件
build: master datanode client cluster-master

# 构建主节点
master:
	@echo "Building Master Node..."
	go build -o bin/lumeofs-master ./cmd/master

# 构建数据节点
datanode:
	@echo "Building Data Node..."
	go build -o bin/lumeofs-datanode ./cmd/datanode

# 构建客户端
client:
	@echo "Building Client..."
	go build -o bin/lumeofs-client ./cmd/client

# 构建集群主节点
cluster-master:
	@echo "Building Cluster Master..."
	go build -o bin/lumeofs-cluster-master ./cmd/cluster-master

# 运行测试
test:
	@echo "Running tests..."
	go test -v ./...

# 清理构建产物
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -rf data/

# 格式化代码
fmt:
	go fmt ./...

# 检查代码
vet:
	go vet ./...

# 整理依赖
tidy:
	go mod tidy

# 启动主节点（开发模式）
run-master:
	go run ./cmd/master -address 0.0.0.0 -port 9000 -replicas 3

# 启动数据节点（开发模式）
run-datanode:
	go run ./cmd/datanode -address 0.0.0.0 -port 9001 -master 127.0.0.1 -master-port 9000

# 帮助信息
help:
	@echo "LumeoFS Build System"
	@echo ""
	@echo "Usage:"
	@echo "  make build      - Build all components"
	@echo "  make master     - Build master node"
	@echo "  make datanode   - Build data node"
	@echo "  make client     - Build client"
	@echo "  make test       - Run tests"
	@echo "  make clean      - Clean build artifacts"
	@echo "  make run-master - Run master in dev mode"
	@echo "  make run-datanode - Run datanode in dev mode"

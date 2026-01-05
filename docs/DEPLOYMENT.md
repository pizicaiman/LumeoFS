# LumeoFS 部署指南

## 目录

- [1. 环境准备](#1-环境准备)
- [2. 单机部署](#2-单机部署)
- [3. 分布式集群部署](#3-分布式集群部署)
- [4. 生产环境部署](#4-生产环境部署)
- [5. 配置详解](#5-配置详解)
- [6. 安全加固](#6-安全加固)
- [7. 监控集成](#7-监控集成)

---

## 1. 环境准备

### 1.1 硬件要求

#### 最小配置（测试环境）

| 组件 | CPU | 内存 | 磁盘 | 网络 |
|------|-----|------|------|------|
| Master | 1 核 | 2GB | 20GB SSD | 100Mbps |
| DataNode | 2 核 | 4GB | 100GB HDD | 1Gbps |

#### 推荐配置（生产环境）

| 组件 | CPU | 内存 | 磁盘 | 网络 |
|------|-----|------|------|------|
| Master | 4 核 | 16GB | 200GB SSD | 10Gbps |
| DataNode | 8 核 | 32GB | 4TB SSD | 10Gbps |

### 1.2 软件依赖

```bash
# 操作系统
Ubuntu 20.04+ / CentOS 7+ / Windows Server 2016+

# Go 环境
Go 1.24.2 或更高版本

# 工具
git, make, gcc (用于编译)
```

### 1.3 网络要求

**端口规划**：

| 服务 | 端口 | 协议 | 说明 |
|------|------|------|------|
| Master HTTP | 9000-9004 | TCP | 客户端 API 端口 |
| Master Raft | 9100-9104 | TCP | Raft 集群通信 |
| DataNode | 10001-10100 | TCP | 数据传输端口 |

**防火墙规则**：

```bash
# 允许 Master 端口
sudo firewall-cmd --permanent --add-port=9000-9004/tcp
sudo firewall-cmd --permanent --add-port=9100-9104/tcp

# 允许 DataNode 端口
sudo firewall-cmd --permanent --add-port=10001-10100/tcp

# 重载防火墙
sudo firewall-cmd --reload
```

---

## 2. 单机部署

适合开发测试，所有组件运行在同一台机器。

### 2.1 下载编译

```bash
# 克隆代码
git clone https://github.com/yourusername/LumeoFS.git
cd LumeoFS

# 编译所有组件
make build

# 验证编译
ls -lh bin/
```

### 2.2 创建配置文件

**Master 配置** (`configs/cluster-master-1.json`):

```json
{
  "node_id": "master-1",
  "address": "127.0.0.1",
  "port": 9000,
  "raft_port": 9100,
  "data_dir": "./data/master-1",
  "replica_count": 3,
  "election_timeout": 3000,
  "heartbeat_interval": 300,
  "heartbeat_period": 3000,
  "peers": [
    {
      "node_id": "master-2",
      "address": "127.0.0.1",
      "port": 9002,
      "raft_port": 9102
    },
    {
      "node_id": "master-3",
      "address": "127.0.0.1",
      "port": 9004,
      "raft_port": 9104
    }
  ]
}
```

**DataNode 配置** (`configs/datanode-1.json`):

```json
{
  "node_id": "datanode-1",
  "address": "127.0.0.1",
  "port": 10001,
  "data_dir": "./data/datanode-1",
  "master_address": "127.0.0.1:9000",
  "heartbeat_interval": 3000,
  "chunk_size": 67108864
}
```

### 2.3 启动服务

```bash
# 创建数据目录
mkdir -p data/master-{1,2,3} data/datanode-{1,2,3}

# 启动 3 个 Master 节点
./bin/cluster-master -config configs/cluster-master-1.json > logs/master-1.log 2>&1 &
./bin/cluster-master -config configs/cluster-master-2.json > logs/master-2.log 2>&1 &
./bin/cluster-master -config configs/cluster-master-3.json > logs/master-3.log 2>&1 &

# 等待 Raft 选举完成（约 5 秒）
sleep 5

# 启动 3 个 DataNode
./bin/datanode -config configs/datanode-1.json > logs/datanode-1.log 2>&1 &
./bin/datanode -config configs/datanode-2.json > logs/datanode-2.log 2>&1 &
./bin/datanode -config configs/datanode-3.json > logs/datanode-3.log 2>&1 &
```

### 2.4 验证部署

```bash
# 检查进程
ps aux | grep cluster-master
ps aux | grep datanode

# 测试上传文件
echo "Hello LumeoFS" > test.txt
./bin/client upload test.txt

# 查看集群状态
./bin/client status
```

---

## 3. 分布式集群部署

生产环境推荐多机部署，提高可用性和性能。

### 3.1 集群拓扑

```
┌─────────────────────────────────────────────────┐
│               负载均衡器 (Optional)              │
│            (HAProxy/Nginx/LVS)                  │
└────────┬───────────────────┬────────────────────┘
         │                   │
    ┌────▼────┐         ┌────▼────┐         ┌────▼────┐
    │ Master-1│         │ Master-2│         │ Master-3│
    │ Server-1│         │ Server-2│         │ Server-3│
    │ 10.0.1.1│         │ 10.0.1.2│         │ 10.0.1.3│
    └─────────┘         └─────────┘         └─────────┘
         │                   │                   │
         └───────────┬───────┴───────────────────┘
                     │
         ┌───────────┼───────────┬───────────┐
         │           │           │           │
    ┌────▼────┐ ┌───▼─────┐ ┌───▼─────┐ ┌───▼─────┐
    │DataNode1│ │DataNode2│ │DataNode3│ │DataNode4│
    │Server-4 │ │Server-5 │ │Server-6 │ │Server-7 │
    │10.0.2.1 │ │10.0.2.2 │ │10.0.2.3 │ │10.0.2.4 │
    └─────────┘ └─────────┘ └─────────┘ └─────────┘
```

### 3.2 服务器分配

| 角色 | 主机名 | IP 地址 | 配置 |
|------|--------|---------|------|
| Master-1 | master1.lumeo.com | 10.0.1.1 | 4C16G 200GB SSD |
| Master-2 | master2.lumeo.com | 10.0.1.2 | 4C16G 200GB SSD |
| Master-3 | master3.lumeo.com | 10.0.1.3 | 4C16G 200GB SSD |
| DataNode-1 | data1.lumeo.com | 10.0.2.1 | 8C32G 4TB SSD |
| DataNode-2 | data2.lumeo.com | 10.0.2.2 | 8C32G 4TB SSD |
| DataNode-3 | data3.lumeo.com | 10.0.2.3 | 8C32G 4TB SSD |
| DataNode-4 | data4.lumeo.com | 10.0.2.4 | 8C32G 4TB SSD |

### 3.3 分布式配置

**Master-1 配置** (`/etc/lumeofs/cluster-master-1.json`):

```json
{
  "node_id": "master-1",
  "address": "10.0.1.1",
  "port": 9000,
  "raft_port": 9100,
  "data_dir": "/var/lib/lumeofs/master-1",
  "replica_count": 3,
  "election_timeout": 3000,
  "heartbeat_interval": 300,
  "heartbeat_period": 3000,
  "peers": [
    {
      "node_id": "master-2",
      "address": "10.0.1.2",
      "port": 9000,
      "raft_port": 9100
    },
    {
      "node_id": "master-3",
      "address": "10.0.1.3",
      "port": 9000,
      "raft_port": 9100
    }
  ]
}
```

**DataNode-1 配置** (`/etc/lumeofs/datanode-1.json`):

```json
{
  "node_id": "datanode-1",
  "address": "10.0.2.1",
  "port": 10001,
  "data_dir": "/data/lumeofs",
  "master_address": "10.0.1.1:9000,10.0.1.2:9000,10.0.1.3:9000",
  "heartbeat_interval": 3000,
  "chunk_size": 67108864,
  "max_chunks": 100000
}
```

### 3.4 系统服务配置

#### Systemd 服务 (Linux)

**Master 服务** (`/etc/systemd/system/lumeofs-master.service`):

```ini
[Unit]
Description=LumeoFS Cluster Master
After=network.target

[Service]
Type=simple
User=lumeofs
Group=lumeofs
WorkingDirectory=/opt/lumeofs
ExecStart=/opt/lumeofs/bin/cluster-master -config /etc/lumeofs/cluster-master-1.json
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

**DataNode 服务** (`/etc/systemd/system/lumeofs-datanode.service`):

```ini
[Unit]
Description=LumeoFS DataNode
After=network.target

[Service]
Type=simple
User=lumeofs
Group=lumeofs
WorkingDirectory=/opt/lumeofs
ExecStart=/opt/lumeofs/bin/datanode -config /etc/lumeofs/datanode-1.json
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

**启动服务**：

```bash
# 重载 systemd
sudo systemctl daemon-reload

# 启用开机自启
sudo systemctl enable lumeofs-master
sudo systemctl enable lumeofs-datanode

# 启动服务
sudo systemctl start lumeofs-master
sudo systemctl start lumeofs-datanode

# 查看状态
sudo systemctl status lumeofs-master
sudo systemctl status lumeofs-datanode
```

### 3.5 部署脚本

创建自动化部署脚本 (`deploy.sh`):

```bash
#!/bin/bash

set -e

# 配置变量
LUMEOFS_VERSION="v1.0.0"
INSTALL_DIR="/opt/lumeofs"
CONFIG_DIR="/etc/lumeofs"
DATA_DIR="/var/lib/lumeofs"
USER="lumeofs"

# 创建用户
if ! id "$USER" &>/dev/null; then
    sudo useradd -r -s /bin/false $USER
fi

# 创建目录
sudo mkdir -p $INSTALL_DIR/{bin,logs}
sudo mkdir -p $CONFIG_DIR
sudo mkdir -p $DATA_DIR

# 下载二进制
wget https://github.com/lumeofs/releases/download/${LUMEOFS_VERSION}/lumeofs-linux-amd64.tar.gz
tar -xzf lumeofs-linux-amd64.tar.gz
sudo mv lumeofs-*/bin/* $INSTALL_DIR/bin/
sudo chmod +x $INSTALL_DIR/bin/*

# 复制配置文件
sudo cp configs/*.json $CONFIG_DIR/

# 设置权限
sudo chown -R $USER:$USER $INSTALL_DIR
sudo chown -R $USER:$USER $DATA_DIR

# 安装 systemd 服务
sudo cp scripts/lumeofs-*.service /etc/systemd/system/
sudo systemctl daemon-reload

echo "LumeoFS 部署完成！"
echo "使用以下命令启动服务："
echo "  sudo systemctl start lumeofs-master"
echo "  sudo systemctl start lumeofs-datanode"
```

---

## 4. 生产环境部署

### 4.1 高可用架构

```
                          ┌─────────────┐
                          │  Keepalived │
                          │     VIP     │
                          │  10.0.0.100 │
                          └──────┬──────┘
                                 │
                    ┌────────────┼────────────┐
                    │                         │
              ┌─────▼─────┐             ┌────▼──────┐
              │ HAProxy-1 │             │ HAProxy-2 │
              │ (Master)  │             │ (Backup)  │
              └─────┬─────┘             └───────────┘
                    │
        ┌───────────┼───────────┬───────────┐
        │           │           │           │
    ┌───▼──┐   ┌───▼──┐   ┌────▼──┐       │
    │Master│   │Master│   │Master │       │
    │  -1  │   │  -2  │   │  -3   │       │
    └──────┘   └──────┘   └───────┘       │
        │           │           │          │
        └───────────┼───────────┘          │
                    │                      │
        ┌───────────┼──────────────────────┘
        │           │           │
    ┌───▼────┐  ┌──▼─────┐ ┌───▼────┐
    │DataNode│  │DataNode│ │DataNode│
    │   -1   │  │   -2   │ │   -3   │
    └────────┘  └────────┘ └────────┘
```

### 4.2 HAProxy 配置

**HAProxy 配置** (`/etc/haproxy/haproxy.cfg`):

```haproxy
global
    log /dev/log local0
    log /dev/log local1 notice
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin
    stats timeout 30s
    user haproxy
    group haproxy
    daemon

defaults
    log     global
    mode    tcp
    option  tcplog
    option  dontlognull
    timeout connect 5000
    timeout client  50000
    timeout server  50000

# LumeoFS Master 集群
frontend lumeofs_master
    bind *:9000
    mode tcp
    default_backend lumeofs_master_nodes

backend lumeofs_master_nodes
    mode tcp
    balance roundrobin
    option tcp-check
    server master1 10.0.1.1:9000 check
    server master2 10.0.1.2:9000 check
    server master3 10.0.1.3:9000 check

# 监控页面
listen stats
    bind *:8080
    stats enable
    stats uri /stats
    stats refresh 5s
    stats auth admin:password
```

### 4.3 Keepalived 配置

**Keepalived Master** (`/etc/keepalived/keepalived.conf`):

```
vrrp_instance VI_1 {
    state MASTER
    interface eth0
    virtual_router_id 51
    priority 100
    advert_int 1
    
    authentication {
        auth_type PASS
        auth_pass lumeofs123
    }
    
    virtual_ipaddress {
        10.0.0.100/24
    }
}
```

### 4.4 磁盘挂载优化

```bash
# 格式化数据盘
sudo mkfs.xfs -f /dev/sdb

# 优化挂载选项
sudo mkdir -p /data/lumeofs
sudo mount -o noatime,nodiratime,nobarrier /dev/sdb /data/lumeofs

# 添加到 /etc/fstab
echo "/dev/sdb /data/lumeofs xfs noatime,nodiratime,nobarrier 0 0" | sudo tee -a /etc/fstab
```

---

## 5. 配置详解

### 5.1 Master 配置参数

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| node_id | string | - | 节点唯一标识 |
| address | string | 0.0.0.0 | 监听地址 |
| port | int | 9000 | HTTP 服务端口 |
| raft_port | int | 9100 | Raft 通信端口 |
| data_dir | string | ./data | 数据目录 |
| replica_count | int | 3 | 默认副本数 |
| election_timeout | int | 3000 | 选举超时（ms） |
| heartbeat_interval | int | 300 | 心跳间隔（ms） |
| heartbeat_period | int | 3000 | 心跳周期（ms） |
| peers | array | [] | 集群节点列表 |

### 5.2 DataNode 配置参数

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| node_id | string | - | 节点唯一标识 |
| address | string | 0.0.0.0 | 监听地址 |
| port | int | 10001 | 服务端口 |
| data_dir | string | ./data | 数据存储目录 |
| master_address | string | - | Master 地址列表 |
| heartbeat_interval | int | 3000 | 心跳间隔（ms） |
| chunk_size | int | 67108864 | Chunk 大小（64MB） |
| max_chunks | int | 100000 | 最大 Chunk 数 |

### 5.3 性能调优参数

```json
{
  "performance": {
    "max_connections": 10000,
    "read_buffer_size": 4096,
    "write_buffer_size": 4096,
    "io_threads": 8,
    "network_threads": 4,
    "gc_interval": 300,
    "compaction_interval": 3600
  }
}
```

---

## 6. 安全加固

### 6.1 TLS 加密

**生成证书**：

```bash
# 生成 CA 证书
openssl genrsa -out ca-key.pem 4096
openssl req -new -x509 -days 3650 -key ca-key.pem -out ca-cert.pem

# 生成服务端证书
openssl genrsa -out server-key.pem 4096
openssl req -new -key server-key.pem -out server.csr
openssl x509 -req -days 3650 -in server.csr -CA ca-cert.pem -CAkey ca-key.pem -CAcreateserial -out server-cert.pem
```

**配置 TLS**：

```json
{
  "tls": {
    "enabled": true,
    "cert_file": "/etc/lumeofs/certs/server-cert.pem",
    "key_file": "/etc/lumeofs/certs/server-key.pem",
    "ca_file": "/etc/lumeofs/certs/ca-cert.pem"
  }
}
```

### 6.2 访问控制

```json
{
  "auth": {
    "enabled": true,
    "token_expire": 3600,
    "users": [
      {
        "username": "admin",
        "password_hash": "sha256:...",
        "role": "admin"
      }
    ]
  }
}
```

### 6.3 网络隔离

```bash
# 仅允许内网访问
sudo iptables -A INPUT -s 10.0.0.0/8 -p tcp --dport 9000 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 9000 -j DROP
```

---

## 7. 监控集成

### 7.1 Prometheus 集成

**配置** (`prometheus.yml`):

```yaml
scrape_configs:
  - job_name: 'lumeofs-master'
    static_configs:
      - targets:
          - '10.0.1.1:9000'
          - '10.0.1.2:9000'
          - '10.0.1.3:9000'
    metrics_path: '/metrics'

  - job_name: 'lumeofs-datanode'
    static_configs:
      - targets:
          - '10.0.2.1:10001'
          - '10.0.2.2:10001'
          - '10.0.2.3:10001'
    metrics_path: '/metrics'
```

### 7.2 Grafana 仪表盘

导入 LumeoFS 官方仪表盘模板：

- **Dashboard ID**: `lumeofs-cluster-overview`
- **数据源**: Prometheus

### 7.3 日志收集

**Filebeat 配置** (`filebeat.yml`):

```yaml
filebeat.inputs:
  - type: log
    enabled: true
    paths:
      - /var/log/lumeofs/*.log
    fields:
      service: lumeofs

output.elasticsearch:
  hosts: ["localhost:9200"]
  index: "lumeofs-%{+yyyy.MM.dd}"
```

---

## 部署检查清单

部署完成后，使用以下清单验证：

- [ ] 所有 Master 节点正常启动
- [ ] Raft 选举完成，有明确的 Leader
- [ ] 所有 DataNode 成功注册到 Master
- [ ] 心跳检测正常工作
- [ ] 文件上传下载功能正常
- [ ] 副本同步正常
- [ ] 防火墙规则配置正确
- [ ] 监控指标正常上报
- [ ] 日志正常输出
- [ ] 数据目录权限正确
- [ ] 系统服务开机自启

---

## 故障排查

### 常见问题

**1. Master 无法选举 Leader**

```bash
# 检查日志
tail -f /var/log/lumeofs/master-1.log

# 检查端口
netstat -an | grep 9100

# 检查防火墙
sudo firewall-cmd --list-all
```

**2. DataNode 无法连接 Master**

```bash
# 测试连接
telnet 10.0.1.1 9000

# 检查 Master 地址配置
cat /etc/lumeofs/datanode-1.json | grep master_address
```

**3. 性能问题**

```bash
# 检查磁盘 I/O
iostat -x 1

# 检查网络带宽
iftop

# 检查进程资源
top -p $(pgrep cluster-master)
```

---

## 总结

本文档涵盖了 LumeoFS 从单机到分布式集群的完整部署流程。生产环境建议：

- 至少 3 个 Master 节点保证高可用
- 至少 4 个 DataNode 节点保证容错
- 配置负载均衡和 VIP 提高可用性
- 启用 TLS 加密和访问控制
- 集成监控和日志系统

更多帮助请参考 [运维手册](OPERATIONS.md)。

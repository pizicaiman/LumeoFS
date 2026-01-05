# LumeoFS 性能优化指南

## 目录

- [1. 性能基准](#1-性能基准)
- [2. 系统调优](#2-系统调优)
- [3. 网络优化](#3-网络优化)
- [4. 存储优化](#4-存储优化)
- [5. 应用层优化](#5-应用层优化)
- [6. 分布式优化](#6-分布式优化)
- [7. 监控与诊断](#7-监控与诊断)

---

## 1. 性能基准

### 1.1 测试环境

**硬件配置**：
```
Master 节点 × 3:
- CPU: Intel Xeon E5-2680 v4 @ 2.4GHz (4 核)
- 内存: 16GB DDR4
- 磁盘: 200GB NVMe SSD
- 网络: 10Gbps

DataNode 节点 × 10:
- CPU: Intel Xeon E5-2680 v4 @ 2.4GHz (8 核)
- 内存: 32GB DDR4
- 磁盘: 4TB NVMe SSD
- 网络: 10Gbps
```

### 1.2 性能指标

#### 吞吐量测试

| 操作 | 文件大小 | 吞吐量 | IOPS |
|------|---------|--------|------|
| 顺序写入 | 64MB | 850 MB/s | 13 |
| 顺序读取 | 64MB | 1200 MB/s | 18 |
| 随机写入 | 4KB | 45 MB/s | 11520 |
| 随机读取 | 4KB | 120 MB/s | 30720 |
| 混合读写 | 64MB | 600 MB/s | 9 |

#### 延迟测试

| 操作 | P50 | P95 | P99 | P99.9 |
|------|-----|-----|-----|-------|
| 写入 | 5ms | 12ms | 25ms | 50ms |
| 读取 | 2ms | 8ms | 15ms | 30ms |
| 元数据查询 | 1ms | 3ms | 8ms | 20ms |

#### 并发测试

| 并发数 | 写入吞吐 | 读取吞吐 | 平均延迟 |
|--------|---------|---------|---------|
| 10 | 350 MB/s | 800 MB/s | 3ms |
| 50 | 700 MB/s | 1500 MB/s | 8ms |
| 100 | 850 MB/s | 2000 MB/s | 15ms |
| 200 | 900 MB/s | 2200 MB/s | 30ms |

### 1.3 性能测试工具

**使用 fio 测试**：

```bash
# 顺序写入测试
fio --name=seq-write \
    --ioengine=libaio \
    --direct=1 \
    --bs=64m \
    --iodepth=32 \
    --rw=write \
    --size=10g \
    --numjobs=4 \
    --directory=/data/lumeofs

# 随机读取测试
fio --name=rand-read \
    --ioengine=libaio \
    --direct=1 \
    --bs=4k \
    --iodepth=64 \
    --rw=randread \
    --size=10g \
    --numjobs=8 \
    --directory=/data/lumeofs
```

**LumeoFS 基准测试**：

```bash
# 使用内置基准测试工具
./bin/lumeofs-bench \
    --master 127.0.0.1:9000 \
    --threads 100 \
    --duration 60s \
    --operation write \
    --file-size 64m

# 混合负载测试
./bin/lumeofs-bench \
    --master 127.0.0.1:9000 \
    --threads 100 \
    --duration 300s \
    --operation mixed \
    --read-ratio 70 \
    --write-ratio 30
```

---

## 2. 系统调优

### 2.1 Linux 内核参数

**网络优化** (`/etc/sysctl.conf`):

```ini
# TCP 缓冲区
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 87380 16777216
net.ipv4.tcp_wmem = 4096 65536 16777216

# 连接队列
net.core.somaxconn = 32768
net.ipv4.tcp_max_syn_backlog = 8192

# TIME_WAIT 优化
net.ipv4.tcp_tw_reuse = 1
net.ipv4.tcp_fin_timeout = 30

# 拥塞控制
net.ipv4.tcp_congestion_control = bbr

# 文件句柄
fs.file-max = 2097152

# AIO 限制
fs.aio-max-nr = 1048576
```

**应用配置**：

```bash
# 立即生效
sudo sysctl -p

# 验证
sysctl net.ipv4.tcp_congestion_control
```

### 2.2 文件系统优化

**XFS 挂载选项**：

```bash
# /etc/fstab
/dev/sdb /data/lumeofs xfs noatime,nodiratime,nobarrier,logbufs=8,logbsize=256k,largeio,inode64,swalloc 0 0
```

**EXT4 挂载选项**：

```bash
# /etc/fstab
/dev/sdb /data/lumeofs ext4 noatime,nodiratime,data=writeback,barrier=0,commit=60 0 0
```

**禁用访问时间更新**：

```bash
# 全局禁用
echo "vm.dirty_ratio = 40" >> /etc/sysctl.conf
echo "vm.dirty_background_ratio = 10" >> /etc/sysctl.conf
sysctl -p
```

### 2.3 内存管理

**调整 dirty 页阈值**：

```bash
# 增大脏页比例，提高写入性能
echo 40 > /proc/sys/vm/dirty_ratio
echo 10 > /proc/sys/vm/dirty_background_ratio

# 缩短刷盘间隔（默认 30 秒）
echo 15 > /proc/sys/vm/dirty_expire_centisecs
echo 5 > /proc/sys/vm/dirty_writeback_centisecs
```

**Huge Pages**：

```bash
# 启用透明大页
echo always > /sys/kernel/mm/transparent_hugepage/enabled
echo always > /sys/kernel/mm/transparent_hugepage/defrag

# 或配置固定大页
echo 2048 > /sys/kernel/mm/hugepages/hugepages-2048kB/nr_hugepages
```

### 2.4 磁盘调度器

**NVMe SSD**：

```bash
# 使用 none 调度器（默认）
echo none > /sys/block/nvme0n1/queue/scheduler
```

**SATA SSD**：

```bash
# 使用 deadline 或 noop
echo deadline > /sys/block/sda/queue/scheduler
```

**机械硬盘**：

```bash
# 使用 cfq
echo cfq > /sys/block/sda/queue/scheduler
```

---

## 3. 网络优化

### 3.1 网卡优化

**调整 Ring Buffer**：

```bash
# 查看当前设置
ethtool -g eth0

# 增大接收缓冲区
ethtool -G eth0 rx 4096 tx 4096
```

**启用多队列**：

```bash
# 检查网卡队列数
ethtool -l eth0

# 设置为 CPU 核心数
ethtool -L eth0 combined 8
```

**中断绑定**：

```bash
# 将网卡中断绑定到特定 CPU
echo 2 > /proc/irq/<IRQ_NUM>/smp_affinity
```

### 3.2 TCP 优化

**启用 TCP BBR**：

```bash
# 加载模块
modprobe tcp_bbr
echo "tcp_bbr" >> /etc/modules-load.d/modules.conf

# 设置拥塞控制
echo "net.ipv4.tcp_congestion_control=bbr" >> /etc/sysctl.conf
echo "net.core.default_qdisc=fq" >> /etc/sysctl.conf
sysctl -p
```

**窗口缩放**：

```bash
# 启用窗口缩放
net.ipv4.tcp_window_scaling = 1

# 启用时间戳
net.ipv4.tcp_timestamps = 1

# 启用 SACK
net.ipv4.tcp_sack = 1
```

### 3.3 网络诊断

**查看网络统计**：

```bash
# 查看丢包情况
netstat -s | grep -i drop

# 查看连接状态
ss -s

# 实时监控带宽
iftop -i eth0
```

**抓包分析**：

```bash
# 抓取 LumeoFS 流量
tcpdump -i eth0 -w lumeofs.pcap port 9000 or port 10001

# 分析
wireshark lumeofs.pcap
```

---

## 4. 存储优化

### 4.1 Chunk 大小调优

**选择合适的 Chunk 大小**：

| 场景 | Chunk 大小 | 说明 |
|------|-----------|------|
| 大文件存储 | 128MB - 256MB | 减少元数据开销 |
| 小文件存储 | 4MB - 16MB | 避免空间浪费 |
| 混合场景 | 64MB | 平衡性能和空间 |

**配置示例**：

```json
{
  "chunk_size": 67108864,
  "chunk_cache_size": 1073741824,
  "chunk_prefetch": true,
  "chunk_prefetch_count": 3
}
```

### 4.2 WAL 优化

**配置选项**：

```json
{
  "wal": {
    "sync_mode": "fsync",
    "buffer_size": 4194304,
    "rotation_size": 1073741824,
    "compression": true
  }
}
```

**同步模式**：

| 模式 | 性能 | 可靠性 | 适用场景 |
|------|------|--------|---------|
| `fsync` | 低 | 高 | 金融、关键数据 |
| `fdatasync` | 中 | 中 | 一般业务 |
| `async` | 高 | 低 | 日志、临时数据 |

### 4.3 压缩策略

**启用压缩**：

```json
{
  "compression": {
    "enabled": true,
    "algorithm": "zstd",
    "level": 3,
    "min_size": 1024
  }
}
```

**压缩算法对比**：

| 算法 | 压缩率 | 速度 | CPU 消耗 |
|------|--------|------|---------|
| none | 1.0x | 最快 | 无 |
| lz4 | 2.0x | 快 | 低 |
| zstd | 3.5x | 中 | 中 |
| gzip | 4.0x | 慢 | 高 |

### 4.4 缓存策略

**多级缓存**：

```
┌─────────────────────────────────────┐
│       客户端缓存 (1GB)               │
│       - 最近访问文件                 │
│       - 元数据缓存                   │
└──────────────┬──────────────────────┘
               │
┌──────────────▼──────────────────────┐
│       Master 缓存 (16GB)             │
│       - 文件元数据                   │
│       - 热点 Chunk 信息              │
└──────────────┬──────────────────────┘
               │
┌──────────────▼──────────────────────┐
│       DataNode 缓存 (32GB)           │
│       - 热点 Chunk 数据              │
│       - 读缓存 + 写缓存              │
└──────────────────────────────────────┘
```

**配置示例**：

```json
{
  "cache": {
    "metadata_cache_size": 1073741824,
    "read_cache_size": 2147483648,
    "write_cache_size": 1073741824,
    "eviction_policy": "lru",
    "cache_ttl": 300
  }
}
```

---

## 5. 应用层优化

### 5.1 批量操作

**批量上传**：

```go
// 不推荐：逐个上传
for _, file := range files {
    client.Upload(ctx, file.Name, file.Data)
}

// 推荐：批量上传
client.BatchUpload(ctx, files)
```

**批量下载**：

```go
// 并发下载
fileIDs := []string{"f1", "f2", "f3", "f4", "f5"}
results := make(chan DownloadResult, len(fileIDs))

for _, fid := range fileIDs {
    go func(id string) {
        data, err := client.Download(ctx, id)
        results <- DownloadResult{ID: id, Data: data, Err: err}
    }(fid)
}

// 收集结果
for i := 0; i < len(fileIDs); i++ {
    result := <-results
    // 处理结果
}
```

### 5.2 连接池管理

**连接池配置**：

```go
config := client.Config{
    MaxConnections:    100,
    MinIdleConns:      10,
    MaxIdleTime:       5 * time.Minute,
    ConnectionTimeout: 10 * time.Second,
}
```

**连接复用**：

```go
// 复用客户端连接
var globalClient *client.Client

func init() {
    globalClient, _ = client.NewClient(config)
}

func uploadFile(data []byte) error {
    // 复用全局客户端
    return globalClient.Upload(context.Background(), "file", data)
}
```

### 5.3 异步处理

**异步写入**：

```go
// 异步上传队列
type UploadQueue struct {
    queue chan UploadTask
    wg    sync.WaitGroup
}

func (q *UploadQueue) Start(workers int) {
    for i := 0; i < workers; i++ {
        q.wg.Add(1)
        go q.worker()
    }
}

func (q *UploadQueue) worker() {
    defer q.wg.Done()
    for task := range q.queue {
        // 执行上传
        task.Execute()
    }
}

func (q *UploadQueue) Submit(task UploadTask) {
    q.queue <- task
}
```

### 5.4 预读与预取

**顺序读取优化**：

```go
// 启用预读
client.SetPrefetch(true, 3) // 预读 3 个 Chunk

// 顺序读取
for i := 0; i < 100; i++ {
    chunk := client.ReadChunk(chunkID + i)
    // 处理数据
}
```

---

## 6. 分布式优化

### 6.1 数据局部性

**智能副本放置**：

```
原则：
1. 副本分散在不同机架
2. 主副本选择最近的节点
3. 读取优先选择本地副本
```

**配置策略**：

```json
{
  "replica_placement": {
    "strategy": "rack_aware",
    "locality_preference": true,
    "cross_zone_replicas": 1
  }
}
```

### 6.2 负载均衡

**动态负载均衡**：

```go
// 基于负载的节点选择
func (m *Master) selectDataNode() *DataNode {
    nodes := m.getAvailableNodes()
    
    // 计算负载分数
    scores := make(map[string]float64)
    for _, node := range nodes {
        score := node.CalculateLoadScore()
        scores[node.ID] = score
    }
    
    // 选择负载最低的节点
    return selectMinScore(nodes, scores)
}
```

**负载指标权重**：

```
总分 = 0.4 × CPU使用率 + 0.3 × 磁盘使用率 + 0.2 × 网络使用率 + 0.1 × Chunk数量
```

### 6.3 热点数据处理

**热点检测**：

```go
type HotspotDetector struct {
    accessCount map[ChunkID]int
    threshold   int
}

func (hd *HotspotDetector) IsHotspot(chunkID ChunkID) bool {
    count := hd.accessCount[chunkID]
    return count > hd.threshold
}

// 热点数据增加副本
func (m *Master) handleHotspot(chunkID ChunkID) {
    if hd.IsHotspot(chunkID) {
        m.addExtraReplica(chunkID)
    }
}
```

### 6.4 跨数据中心优化

**多级缓存**：

```
              Internet
                 │
        ┌────────┴────────┐
        │                 │
    Data Center 1    Data Center 2
        │                 │
    ┌───┴───┐         ┌───┴───┐
    │ Cache │         │ Cache │
    └───┬───┘         └───┬───┘
        │                 │
    ┌───┴───┐         ┌───┴───┐
    │Cluster│         │Cluster│
    └───────┘         └───────┘
```

**跨 DC 配置**：

```json
{
  "cross_dc": {
    "enabled": true,
    "replication_priority": "local",
    "wan_bandwidth_limit": 104857600,
    "compression": true
  }
}
```

---

## 7. 监控与诊断

### 7.1 性能指标

**关键指标**：

```
# 吞吐量
rate(lumeofs_bytes_written_total[1m])
rate(lumeofs_bytes_read_total[1m])

# 延迟
histogram_quantile(0.95, lumeofs_write_latency_seconds_bucket)
histogram_quantile(0.99, lumeofs_read_latency_seconds_bucket)

# IOPS
rate(lumeofs_write_ops_total[1m])
rate(lumeofs_read_ops_total[1m])

# 错误率
rate(lumeofs_errors_total[1m]) / rate(lumeofs_requests_total[1m])
```

### 7.2 性能瓶颈诊断

**CPU 瓶颈**：

```bash
# 查看 CPU 使用率
top -H -p $(pgrep cluster-master)

# 性能分析
go tool pprof http://localhost:9000/debug/pprof/profile?seconds=30

# 火焰图
go tool pprof -http=:8080 cpu.prof
```

**内存瓶颈**：

```bash
# 查看内存使用
pmap -x $(pgrep cluster-master)

# 内存分析
go tool pprof http://localhost:9000/debug/pprof/heap
```

**磁盘 I/O 瓶颈**：

```bash
# 查看磁盘 I/O
iostat -x 1 10

# 查看进程 I/O
iotop -p $(pgrep datanode)

# 分析 I/O 模式
blktrace -d /dev/nvme0n1 -o trace
blkparse -i trace
```

**网络瓶颈**：

```bash
# 查看网络流量
iftop -i eth0

# 查看连接状态
ss -tan | awk '{print $1}' | sort | uniq -c

# 网络延迟
ping -c 100 <datanode-ip> | tail -1
```

### 7.3 慢查询分析

**启用慢查询日志**：

```json
{
  "slow_query": {
    "enabled": true,
    "threshold_ms": 100,
    "log_file": "/var/log/lumeofs/slow_query.log"
  }
}
```

**分析慢查询**：

```bash
# 查看慢查询统计
cat /var/log/lumeofs/slow_query.log | \
  jq -r '.operation' | \
  sort | uniq -c | sort -rn

# 查看最慢的 10 个查询
cat /var/log/lumeofs/slow_query.log | \
  jq -r '. | "\(.duration_ms)\t\(.operation)\t\(.file_id)"' | \
  sort -rn | head -10
```

### 7.4 性能测试报告

**生成性能报告**：

```bash
#!/bin/bash

echo "===== LumeoFS 性能测试报告 ====="
echo "测试时间: $(date)"
echo ""

# 1. 写入测试
echo "=== 顺序写入测试 (64MB × 100) ==="
./bin/lumeofs-bench --operation write --file-size 64m --count 100
echo ""

# 2. 读取测试
echo "=== 顺序读取测试 (64MB × 100) ==="
./bin/lumeofs-bench --operation read --file-size 64m --count 100
echo ""

# 3. 并发测试
echo "=== 并发混合测试 (100 线程) ==="
./bin/lumeofs-bench --operation mixed --threads 100 --duration 60s
echo ""

# 4. 延迟测试
echo "=== 延迟测试 ==="
./bin/lumeofs-bench --operation latency --percentiles 50,95,99,99.9
echo ""
```

---

## 性能优化清单

### 立即见效（低风险）

- [ ] 调整 TCP 缓冲区大小
- [ ] 启用 TCP BBR 拥塞控制
- [ ] 禁用文件系统访问时间更新
- [ ] 启用客户端缓存
- [ ] 使用连接池

### 需要测试（中风险）

- [ ] 调整 Chunk 大小
- [ ] 修改 WAL 同步模式
- [ ] 启用数据压缩
- [ ] 调整脏页比例
- [ ] 优化磁盘调度器

### 需要规划（高风险）

- [ ] 更换文件系统（XFS/EXT4）
- [ ] 启用 Huge Pages
- [ ] 跨数据中心部署
- [ ] 架构调整（增加缓存层）
- [ ] 硬件升级（NVMe SSD）

---

## 性能优化案例

### 案例 1：写入性能提升 3 倍

**问题**：写入吞吐量仅 200 MB/s

**分析**：
- 磁盘 I/O 等待高（iowait > 50%）
- WAL 同步频繁

**优化**：
1. WAL 同步模式改为 `fdatasync`
2. 增大 WAL 缓冲区到 4MB
3. 启用批量写入

**结果**：写入吞吐量提升到 650 MB/s

### 案例 2：读取延迟降低 70%

**问题**：P95 读取延迟 50ms

**分析**：
- 无缓存，每次读取都访问磁盘
- 网络 RTT 高

**优化**：
1. 启用 32GB 读缓存
2. 启用 Chunk 预取
3. 优化网络参数

**结果**：P95 延迟降低到 15ms

### 案例 3：并发能力提升 5 倍

**问题**：100 并发时性能急剧下降

**分析**：
- 连接数达到上限
- 锁竞争严重

**优化**：
1. 增大文件句柄限制
2. 使用读写锁替代互斥锁
3. 实现无锁数据结构

**结果**：支持 500+ 并发连接

---

## 总结

性能优化是一个持续的过程，需要：

1. **监控先行**：建立完善的监控体系
2. **数据驱动**：基于实际数据做决策
3. **小步快跑**：逐步优化，验证效果
4. **权衡取舍**：性能、可靠性、成本的平衡

更多信息请参考：
- [系统架构](ARCHITECTURE.md)
- [部署指南](DEPLOYMENT.md)
- [运维手册](OPERATIONS.md)
- [API 文档](API.md)

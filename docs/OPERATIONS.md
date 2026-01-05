# LumeoFS 运维手册

## 目录

- [1. 日常维护](#1-日常维护)
- [2. 故障恢复](#2-故障恢复)
- [3. 备份与还原](#3-备份与还原)
- [4. 性能监控](#4-性能监控)
- [5. 容量管理](#5-容量管理)
- [6. 升级操作](#6-升级操作)
- [7. 常见问题](#7-常见问题)

---

## 1. 日常维护

### 1.1 健康检查

#### 集群状态检查

```bash
# 查看所有 Master 节点
ps aux | grep cluster-master

# Windows 环境
tasklist | findstr cluster-master

# 查看所有 DataNode
ps aux | grep datanode

# 检查端口占用
netstat -an | grep -E "9000|9100|10001"
```

#### 服务状态检查

```bash
# Linux systemd
sudo systemctl status lumeofs-master
sudo systemctl status lumeofs-datanode

# 查看日志
sudo journalctl -u lumeofs-master -f
sudo journalctl -u lumeofs-datanode -f
```

#### 集群信息查询

```bash
# 查看集群状态
./bin/client status

# 查看节点列表
curl http://localhost:9000/api/v1/nodes

# 查看 Raft 状态
curl http://localhost:9000/api/v1/raft/status
```

### 1.2 节点上下线

#### 节点下线（计划维护）

```bash
# 1. 标记节点为维护模式（停止分配新数据）
curl -X POST http://localhost:9000/api/v1/nodes/datanode-1/maintenance

# 2. 等待数据迁移完成（约 10-30 分钟）
curl http://localhost:9000/api/v1/nodes/datanode-1/status

# 3. 停止节点
sudo systemctl stop lumeofs-datanode
# 或
kill <PID>
# Windows
taskkill //PID <PID>

# 4. 验证集群状态
./bin/client status
```

#### 节点上线

```bash
# 1. 启动节点
sudo systemctl start lumeofs-datanode
# 或
./bin/datanode -config configs/datanode-1.json &

# 2. 验证节点注册
curl http://localhost:9000/api/v1/nodes | grep datanode-1

# 3. 退出维护模式
curl -X DELETE http://localhost:9000/api/v1/nodes/datanode-1/maintenance

# 4. 检查心跳
tail -f /var/log/lumeofs/datanode-1.log | grep heartbeat
```

### 1.3 日志管理

#### 日志轮转配置

**logrotate 配置** (`/etc/logrotate.d/lumeofs`):

```
/var/log/lumeofs/*.log {
    daily
    rotate 30
    missingok
    notifempty
    compress
    delaycompress
    sharedscripts
    postrotate
        /usr/bin/killall -SIGUSR1 cluster-master datanode || true
    endscript
}
```

#### 日志查看

```bash
# 查看最新日志
tail -f /var/log/lumeofs/master-1.log

# 搜索错误日志
grep -i error /var/log/lumeofs/*.log

# 按时间过滤
grep "2026-01-05 10:" /var/log/lumeofs/master-1.log

# 查看 Raft 选举日志
grep -i "election\|leader" /var/log/lumeofs/master-*.log
```

### 1.4 性能巡检

**每日巡检脚本** (`daily_check.sh`):

```bash
#!/bin/bash

DATE=$(date +%Y-%m-%d)
REPORT="/var/log/lumeofs/daily_report_${DATE}.txt"

echo "===== LumeoFS 日常巡检报告 =====" > $REPORT
echo "日期: $DATE" >> $REPORT
echo "" >> $REPORT

# 1. 检查进程
echo "--- 进程检查 ---" >> $REPORT
ps aux | grep -E "cluster-master|datanode" | grep -v grep >> $REPORT
echo "" >> $REPORT

# 2. 检查磁盘空间
echo "--- 磁盘空间 ---" >> $REPORT
df -h | grep -E "data|lumeofs" >> $REPORT
echo "" >> $REPORT

# 3. 检查集群状态
echo "--- 集群状态 ---" >> $REPORT
curl -s http://localhost:9000/api/v1/cluster/health >> $REPORT
echo "" >> $REPORT

# 4. 检查错误日志
echo "--- 错误日志（最近 1 小时）---" >> $REPORT
find /var/log/lumeofs -name "*.log" -mmin -60 -exec grep -i "error\|fatal" {} \; >> $REPORT
echo "" >> $REPORT

# 5. 发送报告
# mail -s "LumeoFS 日常巡检报告" admin@example.com < $REPORT
cat $REPORT
```

---

## 2. 故障恢复

### 2.1 Master 节点故障

#### 单个 Master 故障

**现象**：
- Raft 自动选举新 Leader（约 3-6 秒）
- 客户端可能出现短暂连接失败
- 集群继续正常服务

**恢复步骤**：

```bash
# 1. 确认故障节点
curl http://localhost:9000/api/v1/raft/status

# 2. 重启故障节点
sudo systemctl restart lumeofs-master
# 或重新启动
./bin/cluster-master -config configs/cluster-master-1.json &

# 3. 验证恢复
# 查看日志，确认收到心跳
tail -f /var/log/lumeofs/master-1.log | grep "收到心跳"

# 4. 检查 Raft 状态
curl http://localhost:9000/api/v1/raft/status | jq
```

#### 多个 Master 故障（丧失多数派）

**现象**：
- 集群无法选举 Leader
- 写入操作失败
- 只能提供只读服务

**恢复步骤**：

```bash
# 紧急情况：至少恢复 (N+1)/2 个节点

# 1. 快速恢复节点
sudo systemctl start lumeofs-master
# 重复启动直到达到多数派

# 2. 等待选举完成（约 10 秒）
watch -n 1 'curl -s http://localhost:9000/api/v1/raft/status | jq .leader_id'

# 3. 验证集群恢复
./bin/client upload test.txt
```

**预防措施**：
- **关键**：绝不同时维护多个 Master 节点
- 确保至少 2 个 Master 在线再维护第 3 个
- 配置监控告警

### 2.2 DataNode 故障

#### 单个 DataNode 故障

**现象**：
- Master 检测到心跳超时
- 相关 Chunk 的主副本可能不可用
- 从副本自动提升为主副本

**自动恢复流程**：

```
T=0s:  DataNode-1 故障
T=3s:  Master 检测心跳超时
T=5s:  Master 触发副本恢复
       - 选择 DataNode-2 为新主副本
       - 分配 DataNode-4 为新从副本
T=10s: 开始数据同步
T=30s: 副本恢复完成
```

**手动介入**（可选）：

```bash
# 1. 查看受影响的 Chunk
curl http://localhost:9000/api/v1/chunks?node=datanode-1

# 2. 手动触发副本恢复
curl -X POST http://localhost:9000/api/v1/recovery/start

# 3. 监控恢复进度
curl http://localhost:9000/api/v1/recovery/status
```

#### 多个 DataNode 同时故障

**现象**：
- 部分 Chunk 的所有副本不可用
- 相关文件无法读取

**恢复步骤**：

```bash
# 1. 快速恢复节点
sudo systemctl start lumeofs-datanode

# 2. 如果节点无法恢复，使用纠删码恢复
curl -X POST http://localhost:9000/api/v1/recovery/erasure-code

# 3. 检查数据完整性
./bin/client verify <file-id>
```

### 2.3 网络分区

#### 脑裂风险

Raft 协议可以避免脑裂，但需要注意：

```
正常情况（3 节点）：
Zone-A: Master-1 (Leader), Master-2
Zone-B: Master-3

网络分区后：
Zone-A: Master-1 (Leader), Master-2  ← 保持多数派，继续服务
Zone-B: Master-3 (Follower)          ← 无法当选，只读

网络恢复后：
Master-3 接收 Leader 心跳，自动同步
```

**预防措施**：
- 3 节点部署在不同的网络区域
- 确保任意 2 个节点网络互通
- 使用多条网络链路

### 2.4 数据损坏

#### Chunk 数据损坏

**检测**：

```bash
# 1. 定期校验（cron 任务）
0 2 * * * /opt/lumeofs/bin/datanode-verify --checksum

# 2. 手动校验
curl -X POST http://localhost:10001/api/v1/verify/all
```

**恢复**：

```bash
# 1. 从其他副本恢复
curl -X POST http://localhost:9000/api/v1/chunks/<chunk-id>/repair

# 2. 使用纠删码恢复
curl -X POST http://localhost:9000/api/v1/chunks/<chunk-id>/ec-repair
```

#### 元数据损坏

**备份 Raft 日志**：

```bash
# 定期备份
tar -czf raft-backup-$(date +%Y%m%d).tar.gz /var/lib/lumeofs/master-*/raft/
```

**恢复**：

```bash
# 1. 停止所有 Master
sudo systemctl stop lumeofs-master

# 2. 恢复 Raft 日志
tar -xzf raft-backup-20260105.tar.gz -C /var/lib/lumeofs/

# 3. 重启 Master
sudo systemctl start lumeofs-master
```

---

## 3. 备份与还原

### 3.1 元数据备份

#### 自动备份脚本

**备份脚本** (`backup_metadata.sh`):

```bash
#!/bin/bash

BACKUP_DIR="/backup/lumeofs/metadata"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="metadata_backup_${DATE}.tar.gz"

# 创建备份目录
mkdir -p $BACKUP_DIR

# 备份 Raft 日志和元数据
tar -czf ${BACKUP_DIR}/${BACKUP_FILE} \
    /var/lib/lumeofs/master-*/raft/ \
    /var/lib/lumeofs/master-*/metadata/

# 保留最近 30 天的备份
find $BACKUP_DIR -name "metadata_backup_*.tar.gz" -mtime +30 -delete

# 上传到远程存储（可选）
# aws s3 cp ${BACKUP_DIR}/${BACKUP_FILE} s3://lumeofs-backup/

echo "备份完成: ${BACKUP_FILE}"
```

**定时任务** (`crontab -e`):

```cron
# 每天凌晨 2 点备份元数据
0 2 * * * /opt/lumeofs/scripts/backup_metadata.sh

# 每周日凌晨 3 点全量备份数据
0 3 * * 0 /opt/lumeofs/scripts/backup_data.sh
```

### 3.2 数据备份

#### 增量备份

```bash
#!/bin/bash

BACKUP_DIR="/backup/lumeofs/data"
LAST_BACKUP=$(find $BACKUP_DIR -name "*.manifest" | sort | tail -1)

# 使用 rsync 增量备份
rsync -avz --link-dest=$LAST_BACKUP \
    /data/lumeofs/ \
    ${BACKUP_DIR}/backup_$(date +%Y%m%d)/

# 生成清单文件
find ${BACKUP_DIR}/backup_$(date +%Y%m%d) -type f > \
    ${BACKUP_DIR}/backup_$(date +%Y%m%d).manifest
```

#### 快照备份

```bash
# 使用 LVM 快照
sudo lvcreate -L 10G -s -n lumeofs_snap /dev/vg0/lumeofs

# 挂载快照
sudo mkdir -p /mnt/lumeofs_snap
sudo mount /dev/vg0/lumeofs_snap /mnt/lumeofs_snap

# 备份快照
tar -czf /backup/lumeofs_snap_$(date +%Y%m%d).tar.gz /mnt/lumeofs_snap

# 删除快照
sudo umount /mnt/lumeofs_snap
sudo lvremove -f /dev/vg0/lumeofs_snap
```

### 3.3 数据还原

#### 元数据还原

```bash
# 1. 停止集群
sudo systemctl stop lumeofs-master

# 2. 清空现有数据
rm -rf /var/lib/lumeofs/master-*/raft/*
rm -rf /var/lib/lumeofs/master-*/metadata/*

# 3. 解压备份
tar -xzf metadata_backup_20260105.tar.gz -C /

# 4. 重启集群
sudo systemctl start lumeofs-master

# 5. 验证
curl http://localhost:9000/api/v1/cluster/status
```

#### 数据还原

```bash
# 1. 停止 DataNode
sudo systemctl stop lumeofs-datanode

# 2. 还原数据
rsync -avz /backup/lumeofs/data/backup_20260105/ /data/lumeofs/

# 3. 重启 DataNode
sudo systemctl start lumeofs-datanode

# 4. 触发数据校验
curl -X POST http://localhost:10001/api/v1/verify/all
```

### 3.4 灾难恢复演练

**季度演练计划**：

```bash
# 1. 创建测试环境
docker-compose -f docker-compose.dr.yml up -d

# 2. 恢复最新备份
./scripts/restore_backup.sh /backup/latest

# 3. 验证数据完整性
./scripts/verify_data.sh

# 4. 记录演练结果
echo "演练时间: $(date)" >> dr_report.txt
echo "恢复时间: ${RECOVERY_TIME}" >> dr_report.txt
```

---

## 4. 性能监控

### 4.1 关键指标

#### Master 指标

| 指标 | 说明 | 正常范围 | 告警阈值 |
|------|------|----------|----------|
| raft_term | Raft 任期 | 稳定 | 频繁变化 |
| raft_leader | Leader 状态 | 1 个 | 0 或 >1 |
| metadata_count | 元数据数量 | 递增 | - |
| api_latency_ms | API 延迟 | <10ms | >50ms |
| heartbeat_失败率 | 心跳失败率 | <1% | >5% |

#### DataNode 指标

| 指标 | 说明 | 正常范围 | 告警阈值 |
|------|------|----------|----------|
| disk_usage_percent | 磁盘使用率 | <80% | >90% |
| chunk_count | Chunk 数量 | 稳定增长 | - |
| read_throughput_mbps | 读取吞吐 | - | 持续低于 100MB/s |
| write_throughput_mbps | 写入吞吐 | - | 持续低于 50MB/s |
| io_latency_ms | IO 延迟 | <5ms | >20ms |

### 4.2 监控告警

#### Prometheus 告警规则

**告警配置** (`alerts.yml`):

```yaml
groups:
  - name: lumeofs
    interval: 30s
    rules:
      # Raft Leader 缺失
      - alert: LumeoFSNoLeader
        expr: sum(lumeofs_raft_is_leader) == 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "LumeoFS 集群无 Leader"
          description: "Raft 集群超过 1 分钟没有 Leader"

      # DataNode 离线
      - alert: DataNodeDown
        expr: up{job="lumeofs-datanode"} == 0
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "DataNode {{ $labels.instance }} 离线"
          description: "节点已离线超过 2 分钟"

      # 磁盘空间不足
      - alert: DiskSpaceLow
        expr: lumeofs_disk_usage_percent > 90
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "磁盘空间不足"
          description: "节点 {{ $labels.node_id }} 磁盘使用率 {{ $value }}%"

      # API 延迟过高
      - alert: HighAPILatency
        expr: lumeofs_api_latency_ms > 50
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "API 延迟过高"
          description: "API 延迟 {{ $value }}ms，超过阈值"
```

#### 告警通知

**AlertManager 配置** (`alertmanager.yml`):

```yaml
route:
  receiver: 'lumeofs-team'
  group_by: ['alertname', 'cluster']
  group_wait: 10s
  group_interval: 10s
  repeat_interval: 1h

receivers:
  - name: 'lumeofs-team'
    email_configs:
      - to: 'ops@example.com'
        from: 'alertmanager@example.com'
        smarthost: 'smtp.example.com:587'
    webhook_configs:
      - url: 'http://webhook.example.com/lumeofs'
```

### 4.3 性能分析

#### CPU 性能分析

```bash
# 启用 pprof
curl http://localhost:9000/debug/pprof/profile?seconds=30 > cpu.prof

# 分析
go tool pprof cpu.prof
```

#### 内存分析

```bash
# 获取内存快照
curl http://localhost:9000/debug/pprof/heap > mem.prof

# 分析
go tool pprof mem.prof
```

#### 慢查询分析

```bash
# 查看慢请求
curl http://localhost:9000/api/v1/stats/slow-requests

# 输出格式
# {
#   "request_id": "req-123",
#   "method": "GET",
#   "path": "/api/v1/files/123",
#   "duration_ms": 5000,
#   "timestamp": "2026-01-05T10:00:00Z"
# }
```

---

## 5. 容量管理

### 5.1 容量规划

**计算公式**：

```
可用容量 = 总容量 × (1 - 预留比例) / 副本数

示例：
- 总容量: 4TB × 10 节点 = 40TB
- 预留比例: 20%
- 副本数: 3
- 可用容量 = 40TB × 0.8 / 3 = 10.67TB
```

### 5.2 扩容操作

#### 添加 DataNode

```bash
# 1. 准备新节点
ssh new-node
cd /opt/lumeofs

# 2. 配置新节点
cat > /etc/lumeofs/datanode-5.json <<EOF
{
  "node_id": "datanode-5",
  "address": "10.0.2.5",
  "port": 10005,
  "data_dir": "/data/lumeofs",
  "master_address": "10.0.1.1:9000,10.0.1.2:9000,10.0.1.3:9000",
  "heartbeat_interval": 3000
}
EOF

# 3. 启动节点
sudo systemctl start lumeofs-datanode

# 4. 验证注册
curl http://10.0.1.1:9000/api/v1/nodes | grep datanode-5

# 5. 触发数据重平衡（可选）
curl -X POST http://10.0.1.1:9000/api/v1/rebalance/start
```

#### 添加 Master

**警告**：添加 Master 需要更新所有节点配置

```bash
# 1. 更新所有现有 Master 配置，添加新节点到 peers
# 2. 滚动重启现有 Master
# 3. 启动新 Master 节点
# 4. 验证 Raft 集群状态
```

### 5.3 缩容操作

#### 移除 DataNode

```bash
# 1. 标记节点为退役
curl -X POST http://localhost:9000/api/v1/nodes/datanode-5/decommission

# 2. 等待数据迁移（可能需要数小时）
curl http://localhost:9000/api/v1/nodes/datanode-5/decommission-status

# 3. 确认迁移完成后停止节点
sudo systemctl stop lumeofs-datanode

# 4. 从集群移除节点
curl -X DELETE http://localhost:9000/api/v1/nodes/datanode-5
```

---

## 6. 升级操作

### 6.1 滚动升级（推荐）

**升级 DataNode**：

```bash
# 1. 下载新版本
wget https://github.com/lumeofs/releases/download/v1.1.0/lumeofs-linux-amd64.tar.gz
tar -xzf lumeofs-linux-amd64.tar.gz

# 2. 逐个升级节点
for node in datanode-{1..4}; do
    echo "升级 $node"
    
    # 停止节点
    sudo systemctl stop lumeofs-datanode@$node
    
    # 替换二进制
    sudo cp lumeofs-*/bin/datanode /opt/lumeofs/bin/
    
    # 启动节点
    sudo systemctl start lumeofs-datanode@$node
    
    # 等待节点恢复
    sleep 10
    
    # 验证
    curl http://localhost:10001/api/v1/health
done
```

**升级 Master**：

```bash
# 按 Follower -> Leader 顺序升级

# 1. 确认 Leader
LEADER=$(curl -s http://localhost:9000/api/v1/raft/status | jq -r .leader_id)

# 2. 先升级 Follower
for node in master-{1..3}; do
    if [ "$node" != "$LEADER" ]; then
        echo "升级 Follower $node"
        sudo systemctl stop lumeofs-master@$node
        sudo cp lumeofs-*/bin/cluster-master /opt/lumeofs/bin/
        sudo systemctl start lumeofs-master@$node
        sleep 10
    fi
done

# 3. 最后升级 Leader
echo "升级 Leader $LEADER"
# Leader 升级会触发选举，客户端可能有 5-10 秒不可用
sudo systemctl stop lumeofs-master@$LEADER
sudo cp lumeofs-*/bin/cluster-master /opt/lumeofs/bin/
sudo systemctl start lumeofs-master@$LEADER
```

### 6.2 版本兼容性

| 当前版本 | 目标版本 | 兼容性 | 说明 |
|---------|---------|--------|------|
| v1.0.x | v1.1.x | ✅ 兼容 | 支持滚动升级 |
| v1.0.x | v1.2.x | ⚠️ 部分 | 需要停机升级 |
| v1.0.x | v2.0.x | ❌ 不兼容 | 需要数据迁移 |

---

## 7. 常见问题

### 7.1 Master 相关

**Q1: Master 频繁选举怎么办？**

```bash
# 检查网络延迟
ping -c 10 <other-master-ip>

# 调整选举超时（增大到 5000ms）
# 修改配置文件
"election_timeout": 5000

# 重启 Master
sudo systemctl restart lumeofs-master
```

**Q2: Raft 日志过大怎么办？**

```bash
# 触发日志压缩
curl -X POST http://localhost:9000/api/v1/raft/compact

# 或配置自动压缩
"raft_log_max_size": 1073741824  # 1GB
```

### 7.2 DataNode 相关

**Q3: DataNode 心跳失败？**

```bash
# 检查 Master 连接
telnet <master-ip> 9000

# 检查防火墙
sudo firewall-cmd --list-all

# 检查配置
cat /etc/lumeofs/datanode-1.json | grep master_address
```

**Q4: 磁盘空间不足？**

```bash
# 1. 清理过期 WAL 日志
curl -X POST http://localhost:10001/api/v1/wal/cleanup

# 2. 删除已过期文件
curl -X POST http://localhost:9000/api/v1/gc/run

# 3. 扩容磁盘或添加新节点
```

### 7.3 性能相关

**Q5: 写入速度慢？**

```bash
# 1. 检查磁盘 IO
iostat -x 1

# 2. 检查网络带宽
iftop

# 3. 调整并发参数
"max_concurrent_writes": 100

# 4. 启用异步刷盘（降低一致性）
"wal_sync_mode": "async"
```

**Q6: 读取延迟高？**

```bash
# 1. 启用读缓存
"read_cache_size": 1073741824  # 1GB

# 2. 增加读线程
"read_threads": 16

# 3. 使用 SSD 加速
```

---

## 运维最佳实践

1. **定期备份**：每天备份元数据，每周备份数据
2. **监控告警**：配置完善的监控和告警系统
3. **容量规划**：保持 20% 的预留空间
4. **滚动更新**：避免同时维护多个节点
5. **演练恢复**：定期进行灾难恢复演练
6. **文档记录**：记录每次变更和故障处理过程
7. **版本管理**：保持集群版本一致

---

## 运维工具清单

- **监控**: Prometheus + Grafana
- **日志**: ELK Stack / Loki
- **告警**: AlertManager
- **自动化**: Ansible / Terraform
- **备份**: rsync / duplicity
- **性能分析**: pprof / perf

更多信息请参考 [API 文档](API.md) 和 [性能优化指南](PERFORMANCE.md)。

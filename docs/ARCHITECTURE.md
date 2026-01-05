# LumeoFS 系统架构文档

## 目录

- [1. 基本概念](#1-基本概念)
- [2. 技术架构](#2-技术架构)
- [3. 工作原理](#3-工作原理)
- [4. 核心模块详解](#4-核心模块详解)
- [5. 数据流程](#5-数据流程)

---

## 1. 基本概念

### 1.1 什么是 LumeoFS？

LumeoFS 是一个分布式文件存储系统，专为以下场景设计：

- **高可用性**：多节点冗余，单点故障不影响服务
- **数据一致性**：多副本强一致性保证
- **可扩展性**：水平扩展，支持 PB 级数据存储
- **容错能力**：自动故障检测与恢复

### 1.2 核心概念

#### 1.2.1 文件块（Chunk）

文件在 LumeoFS 中会被切分为固定大小的块（默认 64MB）：

```
原始文件：1GB
    ↓
分片存储：
├── Chunk-1 (64MB)
├── Chunk-2 (64MB)
├── ...
└── Chunk-16 (64MB)
```

#### 1.2.2 副本（Replica）

每个 Chunk 会存储多个副本（默认 3 个）：

```
Chunk-1:
├── 主副本 (Primary)   → DataNode-1
├── 从副本 (Secondary) → DataNode-2
└── 从副本 (Secondary) → DataNode-3
```

**副本角色**：
- **主副本（Primary）**：接受读写请求，负责数据同步
- **从副本（Secondary）**：只读，从主副本同步数据

#### 1.2.3 元数据（Metadata）

Master 节点存储文件系统的元数据：

```json
{
  "file_id": "f123",
  "file_name": "example.txt",
  "file_size": 1073741824,
  "chunks": [
    {
      "chunk_id": "c001",
      "size": 67108864,
      "replicas": [
        {"node_id": "dn1", "role": "primary"},
        {"node_id": "dn2", "role": "secondary"},
        {"node_id": "dn3", "role": "secondary"}
      ]
    }
  ]
}
```

#### 1.2.4 Raft 共识

多个 Master 节点通过 Raft 协议保证元数据一致性：

```
Master-1 (Leader)    ←→    Master-2 (Follower)
      ↕                            ↕
Master-3 (Follower)  ←→  Raft Log Replication
```

**Raft 核心机制**：
- **Leader 选举**：自动选举一个 Leader 处理请求
- **日志复制**：Leader 将元数据变更复制到 Follower
- **故障恢复**：Leader 失败后自动选举新 Leader

---

## 2. 技术架构

### 2.1 整体架构图

```
┌─────────────────────────────────────────────────────────────┐
│                        客户端层 (Client)                     │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │  CLI 客户端  │  │  SDK 客户端  │  │  HTTP 客户端 │      │
│  └──────────────┘  └──────────────┘  └──────────────┘      │
└────────────┬─────────────────────────────────┬──────────────┘
             │                                 │
             │    TCP/gRPC Protocol            │
             │                                 │
┌────────────▼─────────────────────────────────▼──────────────┐
│                    元数据层 (Master Cluster)                 │
│  ┌────────────────┐  ┌────────────────┐  ┌────────────────┐ │
│  │   Master-1     │  │   Master-2     │  │   Master-3     │ │
│  │   (Leader)     │  │  (Follower)    │  │  (Follower)    │ │
│  │                │  │                │  │                │ │
│  │ ┌────────────┐ │  │ ┌────────────┐ │  │ ┌────────────┐ │ │
│  │ │ Raft Node  │ │  │ │ Raft Node  │ │  │ │ Raft Node  │ │ │
│  │ └────────────┘ │  │ └────────────┘ │  │ └────────────┘ │ │
│  │ ┌────────────┐ │  │ ┌────────────┐ │  │ ┌────────────┐ │ │
│  │ │  Metadata  │◄├──┤►│  Metadata  │◄├──┤►│  Metadata  │ │ │
│  │ └────────────┘ │  │ └────────────┘ │  │ └────────────┘ │ │
│  └────────────────┘  └────────────────┘  └────────────────┘ │
└────────────┬─────────────────────────────────────────────────┘
             │
             │  Chunk Allocation & Heartbeat
             │
┌────────────▼─────────────────────────────────────────────────┐
│                      数据层 (DataNode Cluster)               │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐   │
│  │ DataNode-1   │    │ DataNode-2   │    │ DataNode-3   │   │
│  │              │    │              │    │              │   │
│  │ ┌──────────┐ │    │ ┌──────────┐ │    │ ┌──────────┐ │   │
│  │ │  Chunk   │ │    │ │  Chunk   │ │    │ │  Chunk   │ │   │
│  │ │  Storage │ │    │ │  Storage │ │    │ │  Storage │ │   │
│  │ └──────────┘ │    │ └──────────┘ │    │ └──────────┘ │   │
│  │ ┌──────────┐ │    │ ┌──────────┐ │    │ ┌──────────┐ │   │
│  │ │   WAL    │ │    │ │   WAL    │ │    │ │   WAL    │ │   │
│  │ └──────────┘ │    │ └──────────┘ │    │ └──────────┘ │   │
│  │ ┌──────────┐ │◄──►│ ┌──────────┐ │◄──►│ ┌──────────┐ │   │
│  │ │Replicator│ │    │ │Replicator│ │    │ │Replicator│ │   │
│  │ └──────────┘ │    │ └──────────┘ │    │ └──────────┘ │   │
│  └──────────────┘    └──────────────┘    └──────────────┘   │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 分层设计

#### **客户端层 (Client Layer)**
- 提供文件操作接口（上传、下载、删除、查询）
- 与 Master 交互获取元数据
- 与 DataNode 直接交互传输数据

#### **元数据层 (Master Layer)**
- 管理文件系统元数据
- Raft 协议保证多节点一致性
- 分配 Chunk 存储位置
- 监控 DataNode 健康状态

#### **数据层 (DataNode Layer)**
- 存储实际数据块
- WAL 保证数据持久化
- 副本间同步数据
- 定期向 Master 发送心跳

### 2.3 网络拓扑

```
Internet/Intranet
        │
        │
┌───────┴──────────────────────────────────────┐
│              Load Balancer                    │
│      (Optional, for production)               │
└───────┬──────────────────────────────────────┘
        │
        ├──────────┬──────────┬──────────┐
        │          │          │          │
    ┌───▼──┐   ┌──▼───┐  ┌───▼──┐      │
    │Master│   │Master│  │Master│      │
    │  -1  │   │  -2  │  │  -3  │      │
    └──────┘   └──────┘  └──────┘      │
        │          │          │          │
        └──────────┼──────────┘          │
                   │                     │
        ┌──────────┼─────────────────────┘
        │          │          │
    ┌───▼───┐  ┌──▼────┐ ┌───▼───┐
    │DataNode│ │DataNode│ │DataNode│
    │   -1   │ │   -2  │ │   -3  │
    └────────┘ └───────┘ └────────┘
```

---

## 3. 工作原理

### 3.1 文件上传流程

```
客户端                Master              DataNode-1       DataNode-2       DataNode-3
  │                     │                     │               │               │
  ├─1.上传请求────────►│                     │               │               │
  │  (file_name,size)   │                     │               │               │
  │                     │                     │               │               │
  │◄─2.返回Chunk分配─┤                     │               │               │
  │  (chunk_id,nodes)   │                     │               │               │
  │                     │                     │               │               │
  ├─3.写入Chunk-1──────┼───────────────────►│               │               │
  │  (data)             │                     │               │               │
  │                     │                     ├─4.写WAL───►WAL              │
  │                     │                     │               │               │
  │                     │                     ├─5.同步副本──►│               │
  │                     │                     │               │               │
  │                     │                     ├─6.同步副本──┼──────────────►│
  │                     │                     │               │               │
  │◄─7.返回成功─────────────────────────────┤               │               │
  │                     │                     │               │               │
  ├─8.通知完成────────►│                     │               │               │
  │                     │                     │               │               │
  │◄─9.确认────────────┤                     │               │               │
```

**详细步骤**：

1. **客户端请求**：发送文件名、大小到 Master
2. **Master 分配**：
   - 计算需要多少个 Chunk
   - 为每个 Chunk 分配 3 个副本位置
   - 返回 Chunk ID 和 DataNode 地址
3. **写入主副本**：客户端直接连接主副本 DataNode
4. **WAL 日志**：主副本先写 WAL，确保数据可恢复
5. **同步副本**：主副本将数据同步到从副本
6. **确认完成**：所有副本写入成功后返回客户端
7. **更新元数据**：客户端通知 Master 写入完成
8. **持久化元数据**：Master 通过 Raft 持久化元数据

### 3.2 文件下载流程

```
客户端                Master              DataNode-1       DataNode-2
  │                     │                     │               │
  ├─1.下载请求────────►│                     │               │
  │  (file_id)          │                     │               │
  │                     │                     │               │
  │◄─2.返回Chunk信息─┤                     │               │
  │  (chunk_list)       │                     │               │
  │                     │                     │               │
  ├─3.读取Chunk-1──────┼───────────────────►│               │
  │                     │                     │               │
  │◄─4.返回数据─────────────────────────────┤               │
  │                     │                     │               │
  ├─5.读取Chunk-2──────┼───────────────────┼──────────────►│
  │                     │                     │               │
  │◄─6.返回数据─────────────────────────────┼───────────────┤
  │                     │                     │               │
  ├─7.合并Chunk────►本地文件                │               │
```

**详细步骤**：

1. **客户端请求**：发送文件 ID 到 Master
2. **Master 返回**：返回文件的 Chunk 列表和副本位置
3. **并行读取**：客户端并行从多个 DataNode 读取 Chunk
4. **数据返回**：DataNode 返回 Chunk 数据
5. **合并重组**：客户端按顺序合并 Chunk 为完整文件

### 3.3 Raft 选举流程

```
时刻 T=0：正常运行
┌──────────┐      ┌──────────┐      ┌──────────┐
│ Master-1 │─心跳→│ Master-2 │─心跳→│ Master-3 │
│ (Leader) │      │(Follower)│      │(Follower)│
└──────────┘      └──────────┘      └──────────┘
     ↓                 ↓                 ↓
   Term=1           Term=1           Term=1

时刻 T=3s：Leader 故障
┌──────────┐      ┌──────────┐      ┌──────────┐
│ Master-1 │      │ Master-2 │      │ Master-3 │
│ (故障)   │      │(Candidate│      │(Follower)│
└──────────┘      └──────────┘      └──────────┘
                       │                 │
                   发起选举              │
                   Term=2                │
                       │                 │
                   请求投票────────────►│
                       │                 │
                   ◄───────────投票同意─┤
                   2票/3节点              

时刻 T=6s：新 Leader 当选
┌──────────┐      ┌──────────┐      ┌──────────┐
│ Master-1 │      │ Master-2 │◄心跳─│ Master-3 │
│ (故障)   │      │ (Leader) │      │(Follower)│
└──────────┘      └──────────┘      └──────────┘
                       ↓                 ↓
                    Term=2            Term=2
```

**选举规则**：

- **选举超时**：3000-6000ms 随机
- **多数派投票**：需要 (N+1)/2 票才能当选
- **任期递增**：每次选举 Term 加 1
- **心跳维持**：Leader 每 300ms 发送心跳

### 3.4 副本同步流程

```
主副本 (Primary)              从副本-1 (Secondary)        从副本-2 (Secondary)
     │                              │                           │
     ├─1.客户端写入数据────►本地存储                           │
     │                              │                           │
     ├─2.写WAL日志────────►WAL                                 │
     │                              │                           │
     ├─3.同步请求─────────────────►│                           │
     │  (chunk_id, data, version)   │                           │
     │                              │                           │
     │                              ├─4.写WAL────►WAL           │
     │                              │                           │
     │                              ├─5.写本地存储               │
     │                              │                           │
     │◄─6.确认成功──────────────────┤                           │
     │                              │                           │
     ├─7.同步请求─────────────────┼──────────────────────────►│
     │  (chunk_id, data, version)   │                           │
     │                              │                           │
     │                              │                         ├─8.写WAL────►WAL
     │                              │                           │
     │                              │                         ├─9.写本地存储
     │                              │                           │
     │◄─10.确认成功─────────────────┼───────────────────────────┤
     │                              │                           │
     ├─11.返回客户端成功
```

**同步保证**：

- **版本控制**：使用向量时钟跟踪版本
- **租约机制**：主副本持有写租约期间独占写权限
- **写入顺序**：先 WAL，再本地存储
- **多数派确认**：至少 2/3 副本写入成功

---

## 4. 核心模块详解

### 4.1 Raft 模块

**位置**：`internal/raft/raft.go`

**核心结构**：

```go
type RaftNode struct {
    // 节点状态
    state       NodeState    // Follower/Candidate/Leader
    currentTerm uint64       // 当前任期
    votedFor    string       // 投票给谁
    leaderID    string       // 当前Leader
    
    // 日志
    logs        []LogEntry   // Raft日志
    commitIndex uint64       // 已提交索引
    lastApplied uint64       // 已应用索引
    
    // Leader维护
    nextIndex   map[string]uint64   // 每个节点的下一个索引
    matchIndex  map[string]uint64   // 每个节点的匹配索引
    
    // 定时器
    electionTimer  *time.Timer
    heartbeatTimer *time.Timer
}
```

**关键方法**：

```go
// Leader选举
func (rn *RaftNode) startElection()

// 发送投票请求
func (rn *RaftNode) requestVote(peerID string)

// 处理投票请求
func (rn *RaftNode) handleVoteRequest(req *VoteRequest)

// 发送心跳
func (rn *RaftNode) sendHeartbeats()

// 追加日志
func (rn *RaftNode) AppendLog(entry LogEntry)
```

### 4.2 副本管理模块

**位置**：`internal/replication/replication.go`

**核心结构**：

```go
type ReplicationManager struct {
    nodeID      string
    role        ReplicaRole  // Primary/Secondary
    peers       []string     // 其他副本节点
    versionCtrl *VersionController
    lease       *LeaseManager
}
```

**写代理机制**：

```go
// 非主副本写入时转发到主副本
func (rm *ReplicationManager) Write(chunkID string, data []byte) error {
    if rm.role != Primary {
        // 转发到主副本
        return rm.forwardToPrimary(chunkID, data)
    }
    
    // 主副本：先本地写入
    if err := rm.writeLocal(chunkID, data); err != nil {
        return err
    }
    
    // 同步到从副本
    return rm.syncToSecondaries(chunkID, data)
}
```

### 4.3 版本控制模块

**位置**：`internal/version/version.go`

**向量时钟**：

```go
type VectorClock struct {
    Clocks map[string]uint64  // node_id -> version
}

// 递增本地时钟
func (vc *VectorClock) Increment(nodeID string) {
    vc.Clocks[nodeID]++
}

// 合并时钟
func (vc *VectorClock) Merge(other *VectorClock) {
    for nodeID, version := range other.Clocks {
        if version > vc.Clocks[nodeID] {
            vc.Clocks[nodeID] = version
        }
    }
}

// 比较版本（检测冲突）
func (vc *VectorClock) Compare(other *VectorClock) Ordering {
    // BEFORE, AFTER, CONCURRENT
}
```

**租约管理**：

```go
type LeaseManager struct {
    leases map[string]*Lease
}

type Lease struct {
    ChunkID   string
    HolderID  string
    ExpiresAt time.Time
}

// 获取租约
func (lm *LeaseManager) AcquireLease(chunkID, nodeID string) error

// 续约
func (lm *LeaseManager) RenewLease(chunkID string) error

// 释放租约
func (lm *LeaseManager) ReleaseLease(chunkID string) error
```

### 4.4 WAL 模块

**位置**：`internal/wal/wal.go`

**日志结构**：

```go
type WALEntry struct {
    Sequence  uint64    // 序列号
    OpType    OpType    // WRITE/DELETE
    ChunkID   string
    Data      []byte
    Checksum  uint32    // 数据校验和
    Timestamp time.Time
}
```

**写入流程**：

```go
// 1. 写入WAL
func (w *WAL) Append(entry *WALEntry) error {
    // 序列化
    data := entry.Serialize()
    
    // 写入文件
    if _, err := w.file.Write(data); err != nil {
        return err
    }
    
    // 强制刷盘（fsync）
    return w.file.Sync()
}

// 2. 应用到存储
func ApplyWAL(entry *WALEntry) error

// 3. 定期清理已应用的日志
func (w *WAL) Truncate(sequence uint64) error
```

### 4.5 纠删码模块

**位置**：`internal/erasure/erasure.go`

**Reed-Solomon 编码**：

```go
type ErasureEncoder struct {
    dataShards   int  // 数据分片数（默认4）
    parityShards int  // 校验分片数（默认2）
}

// 编码：将数据分片并生成校验分片
func (e *ErasureEncoder) Encode(data []byte) ([][]byte, error) {
    shards := make([][]byte, e.dataShards+e.parityShards)
    
    // 数据分片
    for i := 0; i < e.dataShards; i++ {
        shards[i] = data[i*shardSize : (i+1)*shardSize]
    }
    
    // 生成校验分片
    enc, _ := reedsolomon.New(e.dataShards, e.parityShards)
    enc.Encode(shards)
    
    return shards, nil
}

// 解码：从部分分片恢复完整数据
func (e *ErasureEncoder) Decode(shards [][]byte) ([]byte, error) {
    enc, _ := reedsolomon.New(e.dataShards, e.parityShards)
    
    // 重建丢失的分片
    if err := enc.Reconstruct(shards); err != nil {
        return nil, err
    }
    
    // 合并数据分片
    return joinShards(shards[:e.dataShards]), nil
}
```

---

## 5. 数据流程

### 5.1 读流程详解

```
┌──────────┐
│  Client  │
└────┬─────┘
     │ 1. 请求文件元数据 (file_id)
     ▼
┌─────────────┐
│   Master    │
│  (Leader)   │
└────┬────────┘
     │ 2. 返回Chunk列表和副本位置
     │    [{chunk_id: c1, nodes: [dn1,dn2,dn3]}, ...]
     ▼
┌──────────┐
│  Client  │────3. 选择最近的副本
└────┬─────┘
     │
     ├──────4a. 读Chunk-1──────►┌────────────┐
     │                          │ DataNode-1 │
     │                          └──────┬─────┘
     │                                 │ 5a. 返回数据
     │◄────────────────────────────────┘
     │
     ├──────4b. 读Chunk-2──────►┌────────────┐
     │                          │ DataNode-2 │
     │                          └──────┬─────┘
     │                                 │ 5b. 返回数据
     │◄────────────────────────────────┘
     │
     ├──────4c. 读Chunk-3──────►┌────────────┐
     │                          │ DataNode-3 │
     │                          └──────┬─────┘
     │                                 │ 5c. 返回数据
     │◄────────────────────────────────┘
     │
     └────6. 合并Chunk成完整文件
```

**优化策略**：

- **并行读取**：同时从多个 DataNode 读取不同 Chunk
- **就近选择**：优先选择网络延迟最小的副本
- **负载均衡**：轮询选择不同副本分散负载
- **故障转移**：某个副本不可用时自动切换到其他副本

### 5.2 写流程详解

```
┌──────────┐
│  Client  │
└────┬─────┘
     │ 1. 请求上传 (file_name, size)
     ▼
┌─────────────┐
│   Master    │──────2. Raft日志复制────►┌─────────────┐
│  (Leader)   │◄────3. 多数派确认─────────┤  Followers  │
└────┬────────┘                          └─────────────┘
     │ 4. 分配Chunk和副本位置
     │    [{chunk_id: c1, primary: dn1, secondaries: [dn2,dn3]}]
     ▼
┌──────────┐
│  Client  │
└────┬─────┘
     │ 5. 写Chunk-1到主副本
     ▼
┌────────────────┐
│  DataNode-1    │
│  (Primary)     │
├────────────────┤
│ 6. 写WAL       │
│ 7. 写本地存储   │
└────┬───────────┘
     │ 8. 同步到从副本
     ├───────────►┌────────────────┐
     │            │  DataNode-2    │
     │            │  (Secondary)   │
     │            ├────────────────┤
     │            │ 9. 写WAL       │
     │            │ 10. 写本地存储  │
     │            └────┬───────────┘
     │                 │ 11. 确认
     │◄────────────────┘
     │
     ├───────────►┌────────────────┐
     │            │  DataNode-3    │
     │            │  (Secondary)   │
     │            ├────────────────┤
     │            │ 12. 写WAL      │
     │            │ 13. 写本地存储  │
     │            └────┬───────────┘
     │                 │ 14. 确认
     │◄────────────────┘
     │
     │ 15. 所有副本确认
     ▼
┌──────────┐
│  Client  │───16. 通知完成──►┌─────────────┐
└──────────┘                  │   Master    │
                              └─────────────┘
```

**一致性保证**：

- **先 WAL 后存储**：确保数据可恢复
- **多数派写入**：至少 2/3 副本写入成功
- **版本递增**：每次写入版本号加 1
- **租约保护**：写入时必须持有有效租约

### 5.3 故障恢复流程

#### 场景 1：Master Leader 故障

```
T=0s: 正常运行
Master-1 (Leader) → Master-2 (Follower) → Master-3 (Follower)

T=3s: Master-1故障，停止发送心跳

T=6s: Master-2心跳超时，发起选举
Master-2: Term = 2, State = Candidate
Master-2 → Master-3: 请求投票

T=6.1s: Master-3投票给Master-2
Master-2收到2/3票（自己+Master-3）

T=6.2s: Master-2当选Leader
Master-2 (Leader) → Master-3 (Follower)
开始发送心跳，集群恢复正常

T=10s: Master-1恢复上线
Master-1收到Master-2心跳（Term=2 > 1）
Master-1自动降级为Follower
```

#### 场景 2：DataNode 主副本故障

```
T=0s: 正常运行
Chunk-1: Primary=DN1, Secondaries=[DN2,DN3]

T=3s: DN1故障

T=6s: Master检测到DN1心跳超时

T=6.1s: Master发起副本恢复
选择DN2作为新主副本
通知DN2、DN3角色变更

T=6.2s: 副本角色更新
Chunk-1: Primary=DN2, Secondaries=[DN3]

T=7s: Master分配新副本
选择DN4作为新从副本
DN2同步数据到DN4

T=20s: 副本恢复完成
Chunk-1: Primary=DN2, Secondaries=[DN3,DN4]
```

---

## 总结

LumeoFS 通过以下设计实现高可用分布式存储：

1. **多副本 + Raft**：数据和元数据都有多副本保护
2. **主从架构 + WAL**：保证数据一致性和持久化
3. **版本控制 + 租约**：解决并发写冲突
4. **纠删码**：降低存储成本同时保证容错
5. **自动故障恢复**：最小化人工干预

系统设计充分考虑了 CAP 理论，在保证一致性（C）的前提下，通过 Raft 和副本机制最大化可用性（A），并支持分区容错（P）。

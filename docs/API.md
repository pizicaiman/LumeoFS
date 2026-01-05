# LumeoFS API 文档

## 目录

- [1. 概述](#1-概述)
- [2. 客户端 SDK](#2-客户端-sdk)
- [3. HTTP API](#3-http-api)
- [4. TCP 协议](#4-tcp-协议)
- [5. Go SDK 示例](#5-go-sdk-示例)
- [6. Python SDK 示例](#6-python-sdk-示例)
- [7. 错误码](#7-错误码)

---

## 1. 概述

LumeoFS 提供多种接口方式：

- **TCP 协议**：高性能二进制协议（推荐）
- **HTTP API**：RESTful API，易于集成
- **Go SDK**：原生 Go 语言客户端库
- **Python SDK**：Python 语言客户端库

### 1.1 认证方式

```go
// API Token 认证
Authorization: Bearer <token>

// 基础认证
Authorization: Basic <base64(username:password)>
```

### 1.2 请求/响应格式

**请求**：
```
Content-Type: application/json
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": {...}
}
```

---

## 2. 客户端 SDK

### 2.1 Go SDK

**安装**：

```bash
go get github.com/lumeofs/lumeofs-go-sdk
```

**快速开始**：

```go
package main

import (
    "context"
    "fmt"
    "github.com/lumeofs/lumeofs-go-sdk/client"
)

func main() {
    // 创建客户端
    c, err := client.NewClient(client.Config{
        MasterAddr: []string{"127.0.0.1:9000", "127.0.0.1:9002"},
        Timeout:    30 * time.Second,
    })
    if err != nil {
        panic(err)
    }
    defer c.Close()

    // 上传文件
    fileID, err := c.Upload(context.Background(), "test.txt", []byte("Hello LumeoFS"))
    if err != nil {
        panic(err)
    }
    fmt.Printf("文件上传成功: %s\n", fileID)

    // 下载文件
    data, err := c.Download(context.Background(), fileID)
    if err != nil {
        panic(err)
    }
    fmt.Printf("文件内容: %s\n", string(data))
}
```

### 2.2 Python SDK

**安装**：

```bash
pip install lumeofs
```

**快速开始**：

```python
from lumeofs import Client

# 创建客户端
client = Client(master_addrs=['127.0.0.1:9000', '127.0.0.1:9002'])

# 上传文件
file_id = client.upload('test.txt', b'Hello LumeoFS')
print(f'文件上传成功: {file_id}')

# 下载文件
data = client.download(file_id)
print(f'文件内容: {data.decode()}')

# 关闭客户端
client.close()
```

---

## 3. HTTP API

### 3.1 文件操作

#### 上传文件

**请求**：

```http
POST /api/v1/files
Content-Type: multipart/form-data

--boundary
Content-Disposition: form-data; name="file"; filename="test.txt"
Content-Type: text/plain

Hello LumeoFS
--boundary--
```

**响应**：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "file_id": "f_1a2b3c4d5e6f",
    "file_name": "test.txt",
    "file_size": 14,
    "chunks": [
      {
        "chunk_id": "c_abc123",
        "size": 14,
        "checksum": "sha256:..."
      }
    ],
    "created_at": "2026-01-05T10:00:00Z"
  }
}
```

**cURL 示例**：

```bash
curl -X POST http://localhost:9000/api/v1/files \
  -F "file=@test.txt"
```

#### 下载文件

**请求**：

```http
GET /api/v1/files/{file_id}
```

**响应**：

```
Content-Type: application/octet-stream
Content-Length: 14
Content-Disposition: attachment; filename="test.txt"

Hello LumeoFS
```

**cURL 示例**：

```bash
curl -X GET http://localhost:9000/api/v1/files/f_1a2b3c4d5e6f \
  -o downloaded.txt
```

#### 删除文件

**请求**：

```http
DELETE /api/v1/files/{file_id}
```

**响应**：

```json
{
  "code": 0,
  "message": "success"
}
```

#### 查询文件信息

**请求**：

```http
GET /api/v1/files/{file_id}/info
```

**响应**：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "file_id": "f_1a2b3c4d5e6f",
    "file_name": "test.txt",
    "file_size": 14,
    "chunk_count": 1,
    "replica_count": 3,
    "created_at": "2026-01-05T10:00:00Z",
    "modified_at": "2026-01-05T10:00:00Z"
  }
}
```

#### 列出文件

**请求**：

```http
GET /api/v1/files?page=1&page_size=10&prefix=test
```

**响应**：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "total": 100,
    "page": 1,
    "page_size": 10,
    "files": [
      {
        "file_id": "f_1a2b3c4d5e6f",
        "file_name": "test.txt",
        "file_size": 14,
        "created_at": "2026-01-05T10:00:00Z"
      }
    ]
  }
}
```

### 3.2 集群管理

#### 查询集群状态

**请求**：

```http
GET /api/v1/cluster/status
```

**响应**：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "cluster_id": "lumeo-cluster-1",
    "version": "v1.0.0",
    "master_count": 3,
    "datanode_count": 10,
    "total_capacity": 10995116277760,
    "used_capacity": 1099511627776,
    "available_capacity": 9895604649984,
    "file_count": 10000,
    "chunk_count": 50000,
    "status": "healthy"
  }
}
```

#### 查询节点列表

**请求**：

```http
GET /api/v1/nodes?type=datanode&status=online
```

**响应**：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "nodes": [
      {
        "node_id": "datanode-1",
        "address": "10.0.2.1:10001",
        "status": "online",
        "capacity": 1099511627776,
        "used": 109951162777,
        "available": 989560464998,
        "chunk_count": 5000,
        "last_heartbeat": "2026-01-05T10:00:00Z"
      }
    ]
  }
}
```

#### 查询 Raft 状态

**请求**：

```http
GET /api/v1/raft/status
```

**响应**：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "node_id": "master-1",
    "state": "Leader",
    "term": 2,
    "leader_id": "master-1",
    "commit_index": 1000,
    "last_applied": 1000,
    "peers": [
      {
        "node_id": "master-2",
        "address": "10.0.1.2:9100",
        "next_index": 1001,
        "match_index": 1000
      },
      {
        "node_id": "master-3",
        "address": "10.0.1.3:9100",
        "next_index": 1001,
        "match_index": 1000
      }
    ]
  }
}
```

### 3.3 运维操作

#### 节点维护模式

**启用维护模式**：

```http
POST /api/v1/nodes/{node_id}/maintenance
```

**退出维护模式**：

```http
DELETE /api/v1/nodes/{node_id}/maintenance
```

#### 触发副本恢复

**请求**：

```http
POST /api/v1/recovery/start
Content-Type: application/json

{
  "chunk_ids": ["c_abc123", "c_def456"],
  "priority": "high"
}
```

#### 数据重平衡

**请求**：

```http
POST /api/v1/rebalance/start
Content-Type: application/json

{
  "strategy": "balance_by_capacity",
  "max_migration_rate": 104857600
}
```

**查询重平衡状态**：

```http
GET /api/v1/rebalance/status
```

**响应**：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "status": "running",
    "progress": 45.5,
    "migrated_chunks": 100,
    "total_chunks": 220,
    "started_at": "2026-01-05T10:00:00Z",
    "estimated_completion": "2026-01-05T11:30:00Z"
  }
}
```

---

## 4. TCP 协议

### 4.1 协议格式

```
┌──────────────────────────────────────────────┐
│              消息头 (24 字节)                 │
├──────────────────────────────────────────────┤
│ Magic (4 字节): 0x4C4D4F46 ("LMOF")         │
│ Version (2 字节): 协议版本                   │
│ Type (2 字节): 消息类型                      │
│ Length (8 字节): 消息体长度                  │
│ RequestID (8 字节): 请求 ID                  │
├──────────────────────────────────────────────┤
│              消息体 (可变长度)                │
│              JSON / Protocol Buffers         │
└──────────────────────────────────────────────┘
```

### 4.2 消息类型

| 类型码 | 名称 | 说明 |
|-------|------|------|
| 0x0001 | UPLOAD_REQUEST | 上传文件请求 |
| 0x0002 | UPLOAD_RESPONSE | 上传文件响应 |
| 0x0003 | DOWNLOAD_REQUEST | 下载文件请求 |
| 0x0004 | DOWNLOAD_RESPONSE | 下载文件响应 |
| 0x0005 | DELETE_REQUEST | 删除文件请求 |
| 0x0006 | DELETE_RESPONSE | 删除文件响应 |
| 0x0007 | LIST_REQUEST | 列表查询请求 |
| 0x0008 | LIST_RESPONSE | 列表查询响应 |
| 0x0010 | HEARTBEAT_REQUEST | 心跳请求 |
| 0x0011 | HEARTBEAT_RESPONSE | 心跳响应 |

### 4.3 协议示例

**上传请求**：

```json
{
  "type": "UPLOAD_REQUEST",
  "request_id": 12345,
  "data": {
    "file_name": "test.txt",
    "file_size": 14,
    "file_content": "SGVsbG8gTHVtZW9GUw=="  // Base64 编码
  }
}
```

**上传响应**：

```json
{
  "type": "UPLOAD_RESPONSE",
  "request_id": 12345,
  "data": {
    "file_id": "f_1a2b3c4d5e6f",
    "chunks": [
      {
        "chunk_id": "c_abc123",
        "replicas": [
          {
            "node_id": "datanode-1",
            "address": "10.0.2.1:10001",
            "role": "primary"
          }
        ]
      }
    ]
  }
}
```

---

## 5. Go SDK 示例

### 5.1 完整示例

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"
    
    "github.com/lumeofs/lumeofs-go-sdk/client"
)

func main() {
    // 创建客户端
    c, err := client.NewClient(client.Config{
        MasterAddr: []string{
            "127.0.0.1:9000",
            "127.0.0.1:9002",
            "127.0.0.1:9004",
        },
        Timeout:        30 * time.Second,
        RetryCount:     3,
        RetryInterval:  time.Second,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer c.Close()

    ctx := context.Background()

    // 1. 上传文件
    fmt.Println("=== 上传文件 ===")
    content := []byte("Hello, LumeoFS!")
    fileID, err := c.Upload(ctx, "hello.txt", content)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("文件上传成功，ID: %s\n", fileID)

    // 2. 查询文件信息
    fmt.Println("\n=== 查询文件信息 ===")
    info, err := c.GetFileInfo(ctx, fileID)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("文件名: %s\n", info.FileName)
    fmt.Printf("大小: %d 字节\n", info.FileSize)
    fmt.Printf("副本数: %d\n", info.ReplicaCount)

    // 3. 下载文件
    fmt.Println("\n=== 下载文件 ===")
    data, err := c.Download(ctx, fileID)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("文件内容: %s\n", string(data))

    // 4. 列出文件
    fmt.Println("\n=== 列出文件 ===")
    files, err := c.ListFiles(ctx, client.ListOptions{
        Prefix:   "hello",
        Page:     1,
        PageSize: 10,
    })
    if err != nil {
        log.Fatal(err)
    }
    for _, f := range files {
        fmt.Printf("- %s (%d 字节)\n", f.FileName, f.FileSize)
    }

    // 5. 删除文件
    fmt.Println("\n=== 删除文件 ===")
    if err := c.Delete(ctx, fileID); err != nil {
        log.Fatal(err)
    }
    fmt.Println("文件删除成功")
}
```

### 5.2 流式上传大文件

```go
func uploadLargeFile(c *client.Client, filePath string) error {
    file, err := os.Open(filePath)
    if err != nil {
        return err
    }
    defer file.Close()

    // 获取文件信息
    stat, err := file.Stat()
    if err != nil {
        return err
    }

    // 创建上传会话
    session, err := c.CreateUploadSession(context.Background(), client.UploadSessionOptions{
        FileName: filepath.Base(filePath),
        FileSize: stat.Size(),
        ChunkSize: 64 * 1024 * 1024, // 64MB per chunk
    })
    if err != nil {
        return err
    }

    // 分块上传
    buffer := make([]byte, 64*1024*1024)
    for {
        n, err := file.Read(buffer)
        if err == io.EOF {
            break
        }
        if err != nil {
            return err
        }

        if err := session.UploadChunk(buffer[:n]); err != nil {
            return err
        }
    }

    // 完成上传
    fileID, err := session.Complete()
    if err != nil {
        return err
    }

    fmt.Printf("大文件上传成功: %s\n", fileID)
    return nil
}
```

### 5.3 断点续传

```go
func resumableUpload(c *client.Client, filePath string) error {
    // 查询未完成的上传会话
    sessions, err := c.ListUploadSessions(context.Background())
    if err != nil {
        return err
    }

    var session *client.UploadSession
    for _, s := range sessions {
        if s.FileName == filepath.Base(filePath) {
            session = s
            break
        }
    }

    if session == nil {
        // 创建新会话
        session, err = c.CreateUploadSession(context.Background(), client.UploadSessionOptions{
            FileName: filepath.Base(filePath),
            FileSize: getFileSize(filePath),
        })
        if err != nil {
            return err
        }
    }

    // 从上次中断的位置继续
    file, err := os.Open(filePath)
    if err != nil {
        return err
    }
    defer file.Close()

    if _, err := file.Seek(session.UploadedSize, 0); err != nil {
        return err
    }

    // 继续上传
    // ...
    return nil
}
```

---

## 6. Python SDK 示例

### 6.1 完整示例

```python
from lumeofs import Client, ClientConfig
import time

# 创建客户端
config = ClientConfig(
    master_addrs=['127.0.0.1:9000', '127.0.0.1:9002'],
    timeout=30,
    retry_count=3
)
client = Client(config)

try:
    # 1. 上传文件
    print("=== 上传文件 ===")
    file_id = client.upload('hello.txt', b'Hello, LumeoFS!')
    print(f"文件上传成功，ID: {file_id}")

    # 2. 查询文件信息
    print("\n=== 查询文件信息 ===")
    info = client.get_file_info(file_id)
    print(f"文件名: {info['file_name']}")
    print(f"大小: {info['file_size']} 字节")

    # 3. 下载文件
    print("\n=== 下载文件 ===")
    data = client.download(file_id)
    print(f"文件内容: {data.decode()}")

    # 4. 列出文件
    print("\n=== 列出文件 ===")
    files = client.list_files(prefix='hello', page=1, page_size=10)
    for f in files:
        print(f"- {f['file_name']} ({f['file_size']} 字节)")

    # 5. 删除文件
    print("\n=== 删除文件 ===")
    client.delete(file_id)
    print("文件删除成功")

finally:
    client.close()
```

### 6.2 异步操作

```python
import asyncio
from lumeofs import AsyncClient

async def main():
    client = AsyncClient(['127.0.0.1:9000'])
    
    try:
        # 并发上传多个文件
        tasks = [
            client.upload(f'file{i}.txt', f'Content {i}'.encode())
            for i in range(10)
        ]
        file_ids = await asyncio.gather(*tasks)
        print(f"上传完成: {file_ids}")
        
        # 并发下载
        tasks = [client.download(fid) for fid in file_ids]
        contents = await asyncio.gather(*tasks)
        
    finally:
        await client.close()

asyncio.run(main())
```

### 6.3 上下文管理器

```python
from lumeofs import Client

# 使用 with 语句自动管理连接
with Client(['127.0.0.1:9000']) as client:
    file_id = client.upload('test.txt', b'Test content')
    data = client.download(file_id)
    print(data.decode())
# 自动关闭连接
```

---

## 7. 错误码

| 错误码 | 名称 | 说明 |
|-------|------|------|
| 0 | SUCCESS | 成功 |
| 1001 | INVALID_PARAMETER | 参数错误 |
| 1002 | FILE_NOT_FOUND | 文件不存在 |
| 1003 | FILE_ALREADY_EXISTS | 文件已存在 |
| 1004 | CHUNK_NOT_FOUND | 数据块不存在 |
| 1005 | NODE_NOT_FOUND | 节点不存在 |
| 2001 | NO_AVAILABLE_NODE | 无可用节点 |
| 2002 | INSUFFICIENT_REPLICAS | 副本数不足 |
| 2003 | REPLICATION_FAILED | 副本同步失败 |
| 3001 | NETWORK_ERROR | 网络错误 |
| 3002 | TIMEOUT | 超时 |
| 3003 | CONNECTION_REFUSED | 连接被拒绝 |
| 4001 | INTERNAL_ERROR | 内部错误 |
| 4002 | RAFT_ERROR | Raft 错误 |
| 4003 | STORAGE_ERROR | 存储错误 |
| 5001 | AUTHENTICATION_FAILED | 认证失败 |
| 5002 | PERMISSION_DENIED | 权限不足 |

### 错误响应格式

```json
{
  "code": 1002,
  "message": "file not found",
  "error": "FILE_NOT_FOUND",
  "details": {
    "file_id": "f_1a2b3c4d5e6f",
    "timestamp": "2026-01-05T10:00:00Z"
  }
}
```

---

## 附录

### A. 完整 API 列表

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/files` | 上传文件 |
| GET | `/api/v1/files/{file_id}` | 下载文件 |
| GET | `/api/v1/files/{file_id}/info` | 查询文件信息 |
| GET | `/api/v1/files` | 列出文件 |
| DELETE | `/api/v1/files/{file_id}` | 删除文件 |
| GET | `/api/v1/cluster/status` | 集群状态 |
| GET | `/api/v1/nodes` | 节点列表 |
| GET | `/api/v1/raft/status` | Raft 状态 |
| POST | `/api/v1/nodes/{node_id}/maintenance` | 启用维护模式 |
| DELETE | `/api/v1/nodes/{node_id}/maintenance` | 退出维护模式 |
| POST | `/api/v1/recovery/start` | 触发副本恢复 |
| POST | `/api/v1/rebalance/start` | 开始重平衡 |
| GET | `/api/v1/rebalance/status` | 重平衡状态 |

### B. 性能优化建议

1. **批量操作**：使用批量上传/下载 API 减少网络往返
2. **连接池**：复用 TCP 连接，减少握手开销
3. **并发控制**：合理设置并发数，避免过载
4. **缓存**：客户端缓存文件元数据
5. **压缩**：启用数据压缩减少传输量

### C. SDK 配置参考

```go
type ClientConfig struct {
    MasterAddr     []string      // Master 地址列表
    Timeout        time.Duration // 请求超时
    RetryCount     int           // 重试次数
    RetryInterval  time.Duration // 重试间隔
    MaxConnections int           // 最大连接数
    EnableCache    bool          // 启用缓存
    CacheSize      int           // 缓存大小
    Compression    bool          // 启用压缩
}
```

---

更多信息请参考：
- [系统架构](ARCHITECTURE.md)
- [部署指南](DEPLOYMENT.md)
- [运维手册](OPERATIONS.md)
- [性能优化](PERFORMANCE.md)

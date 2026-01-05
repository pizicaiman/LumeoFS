// Package common 提供LumeoFS的公共类型定义
package common

import (
	"time"
)

// 副本角色定义
type ReplicaRole int

const (
	RolePrimary   ReplicaRole = iota // 主副本：负责读写
	RoleSecondary                    // 从副本：只读
)

// 默认配置
const (
	DefaultReplicaCount    = 3                // 默认3副本
	DefaultErasureShards   = 3                // 纠错码分片数
	DefaultChunkSize       = 64 * 1024 * 1024 // 64MB分片大小
	DefaultHeartbeatPeriod = 3 * time.Second  // 心跳周期
)

// ChunkID 数据块唯一标识
type ChunkID string

// NodeID 节点唯一标识
type NodeID string

// FileID 文件唯一标识
type FileID string

// ChunkInfo 数据块信息
type ChunkInfo struct {
	ID         ChunkID       `json:"id"`
	FileID     FileID        `json:"file_id"`
	Index      int           `json:"index"` // 在文件中的序号
	Size       int64         `json:"size"`
	Checksum   string        `json:"checksum"` // 校验和
	Replicas   []ReplicaInfo `json:"replicas"` // 副本列表
	CreateTime time.Time     `json:"create_time"`
}

// ReplicaInfo 副本信息
type ReplicaInfo struct {
	ChunkID    ChunkID       `json:"chunk_id"`
	NodeID     NodeID        `json:"node_id"`
	Role       ReplicaRole   `json:"role"`    // 主/从角色
	Version    int64         `json:"version"` // 数据版本号
	Checksum   string        `json:"checksum"`
	Status     ReplicaStatus `json:"status"`
	UpdateTime time.Time     `json:"update_time"`
}

// ReplicaStatus 副本状态
type ReplicaStatus int

const (
	ReplicaHealthy ReplicaStatus = iota // 健康
	ReplicaSyncing                      // 同步中
	ReplicaStale                        // 过期
	ReplicaOffline                      // 离线
)

// DataNodeInfo 数据节点信息
type DataNodeInfo struct {
	ID            NodeID     `json:"id"`
	Address       string     `json:"address"`
	Port          int        `json:"port"`
	TotalSpace    int64      `json:"total_space"`
	UsedSpace     int64      `json:"used_space"`
	ChunkCount    int        `json:"chunk_count"`
	Status        NodeStatus `json:"status"`
	LastHeartbeat time.Time  `json:"last_heartbeat"`
}

// NodeStatus 节点状态
type NodeStatus int

const (
	NodeOnline NodeStatus = iota
	NodeOffline
)

// FileMetadata 文件元数据
type FileMetadata struct {
	ID         FileID    `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ChunkIDs   []ChunkID `json:"chunk_ids"`
	CreateTime time.Time `json:"create_time"`
	ModifyTime time.Time `json:"modify_time"`
}

// WriteRequest 写请求
type WriteRequest struct {
	FileID   FileID  `json:"file_id"`
	ChunkID  ChunkID `json:"chunk_id"`
	Data     []byte  `json:"data"`
	Offset   int64   `json:"offset"`
	Checksum string  `json:"checksum"`
}

// WriteResponse 写响应
type WriteResponse struct {
	Success bool   `json:"success"`
	Version int64  `json:"version"`
	Error   string `json:"error,omitempty"`
}

// ReadRequest 读请求
type ReadRequest struct {
	ChunkID ChunkID `json:"chunk_id"`
	Offset  int64   `json:"offset"`
	Length  int64   `json:"length"`
}

// ReadResponse 读响应
type ReadResponse struct {
	Data     []byte `json:"data"`
	Checksum string `json:"checksum"`
	Version  int64  `json:"version"`
	Error    string `json:"error,omitempty"`
}

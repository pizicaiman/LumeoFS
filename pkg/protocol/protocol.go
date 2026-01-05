// Package protocol 定义LumeoFS的通信协议
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"io"
)

// 消息类型
type MsgType uint8

const (
	MsgTypeHeartbeat        MsgType = 1   // 心跳
	MsgTypeRegister         MsgType = 2   // 节点注册
	MsgTypePutFile          MsgType = 10  // 上传文件
	MsgTypeGetFile          MsgType = 11  // 下载文件
	MsgTypeDeleteFile       MsgType = 12  // 删除文件
	MsgTypeListDir          MsgType = 13  // 列目录
	MsgTypeGetStatus        MsgType = 20  // 获取状态
	MsgTypeAllocChunk       MsgType = 21  // 分配数据块
	MsgTypeWriteChunk       MsgType = 30  // 写数据块
	MsgTypeReadChunk        MsgType = 31  // 读数据块
	MsgTypeSyncChunk        MsgType = 32  // 同步数据块
	MsgTypeSetRole          MsgType = 40  // 设置副本角色
	MsgTypeGrantLease       MsgType = 41  // 授予租约
	MsgTypeRenewLease       MsgType = 42  // 续约
	MsgTypeGetVersion       MsgType = 50  // 获取版本
	MsgTypeCheckConsistency MsgType = 51  // 一致性检查
	MsgTypeResponse         MsgType = 100 // 响应
)

// Message 通信消息
type Message struct {
	Type    MsgType `json:"type"`
	Payload []byte  `json:"payload"`
}

// Request 请求基类
type Request struct {
	RequestID string `json:"request_id"`
}

// Response 响应基类
type Response struct {
	RequestID string `json:"request_id"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

// PutFileRequest 上传文件请求
type PutFileRequest struct {
	Request
	FileName string `json:"file_name"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
	Checksum string `json:"checksum"`
}

// PutFileResponse 上传文件响应
type PutFileResponse struct {
	Response
	FileID   string   `json:"file_id"`
	ChunkIDs []string `json:"chunk_ids"`
}

// GetFileRequest 下载文件请求
type GetFileRequest struct {
	Request
	FilePath string `json:"file_path"`
}

// GetFileResponse 下载文件响应
type GetFileResponse struct {
	Response
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	Data     []byte `json:"data"`
}

// StatusRequest 状态查询请求
type StatusRequest struct {
	Request
}

// StatusResponse 状态响应
type StatusResponse struct {
	Response
	NodeCount  int          `json:"node_count"`
	TotalSpace int64        `json:"total_space"`
	UsedSpace  int64        `json:"used_space"`
	FileCount  int          `json:"file_count"`
	ChunkCount int          `json:"chunk_count"`
	Nodes      []NodeStatus `json:"nodes"`
}

// NodeStatus 节点状态
type NodeStatus struct {
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
	Status     string `json:"status"`
	ChunkCount int    `json:"chunk_count"`
}

// RegisterRequest 节点注册请求
type RegisterRequest struct {
	Request
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	TotalSpace int64  `json:"total_space"`
	UsedSpace  int64  `json:"used_space"`
}

// RegisterResponse 节点注册响应
type RegisterResponse struct {
	Response
}

// HeartbeatRequest 心跳请求
type HeartbeatRequest struct {
	Request
	NodeID     string `json:"node_id"`
	ChunkCount int    `json:"chunk_count"`
	UsedSpace  int64  `json:"used_space"`
}

// HeartbeatResponse 心跳响应
type HeartbeatResponse struct {
	Response
}

// AllocChunkRequest 分配数据块请求
type AllocChunkRequest struct {
	Request
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
}

// ChunkLocation 数据块位置
type ChunkLocation struct {
	ChunkID string   `json:"chunk_id"`
	Index   int      `json:"index"`
	Size    int64    `json:"size"`
	Nodes   []string `json:"nodes"`   // 节点地址列表
	Primary string   `json:"primary"` // 主副本节点
}

// AllocChunkResponse 分配数据块响应
type AllocChunkResponse struct {
	Response
	Chunks []ChunkLocation `json:"chunks"`
}

// WriteChunkRequest 写数据块请求
type WriteChunkRequest struct {
	Request
	ChunkID  string `json:"chunk_id"`
	Data     []byte `json:"data"`
	Offset   int64  `json:"offset"`
	Checksum string `json:"checksum"`
}

// WriteChunkResponse 写数据块响应
type WriteChunkResponse struct {
	Response
	Version int64 `json:"version"`
}

// ReadChunkRequest 读数据块请求
type ReadChunkRequest struct {
	Request
	ChunkID string `json:"chunk_id"`
	Offset  int64  `json:"offset"`
	Length  int64  `json:"length"`
}

// ReadChunkResponse 读数据块响应
type ReadChunkResponse struct {
	Response
	Data     []byte `json:"data"`
	Checksum string `json:"checksum"`
	Version  int64  `json:"version"`
}

// ===========================================
// 副本同步相关消息
// ===========================================

// SyncChunkRequest 副本同步请求（主副本 -> 从副本）
type SyncChunkRequest struct {
	Request
	ChunkID      string            `json:"chunk_id"`
	Data         []byte            `json:"data"`
	Offset       int64             `json:"offset"`
	Checksum     string            `json:"checksum"`
	Version      int64             `json:"version"`      // 逻辑版本
	VectorClock  map[string]uint64 `json:"vector_clock"` // 向量时钟
	Timestamp    int64             `json:"timestamp"`    // 时间戳
	SourceNodeID string            `json:"source_node_id"`
}

// SyncChunkResponse 副本同步响应
type SyncChunkResponse struct {
	Response
	Version int64 `json:"version"`
}

// ===========================================
// 角色通知相关消息
// ===========================================

// SetRoleRequest 设置副本角色请求（Master -> DataNode）
type SetRoleRequest struct {
	Request
	ChunkID     string            `json:"chunk_id"`
	Role        string            `json:"role"` // "PRIMARY" or "SECONDARY"
	PrimaryNode string            `json:"primary_node,omitempty"`
	Replicas    []ReplicaNodeInfo `json:"replicas,omitempty"`
}

// ReplicaNodeInfo 副本节点信息
type ReplicaNodeInfo struct {
	NodeID  string `json:"node_id"`
	Address string `json:"address"`
	Role    string `json:"role"`
}

// SetRoleResponse 设置角色响应
type SetRoleResponse struct {
	Response
}

// ===========================================
// 租约相关消息
// ===========================================

// GrantLeaseRequest 授予租约请求
type GrantLeaseRequest struct {
	Request
	ChunkID       string `json:"chunk_id"`
	NodeID        string `json:"node_id"`
	LeaseDuration int64  `json:"lease_duration"` // 秒
}

// GrantLeaseResponse 授予租约响应
type GrantLeaseResponse struct {
	Response
	LeaseID   string `json:"lease_id"`
	ExpiresAt int64  `json:"expires_at"` // Unix时间戳
	Version   int64  `json:"version"`
}

// RenewLeaseRequest 续约请求
type RenewLeaseRequest struct {
	Request
	ChunkID string `json:"chunk_id"`
	NodeID  string `json:"node_id"`
	LeaseID string `json:"lease_id"`
}

// RenewLeaseResponse 续约响应
type RenewLeaseResponse struct {
	Response
	ExpiresAt int64 `json:"expires_at"`
}

// ===========================================
// 版本查询相关消息
// ===========================================

// GetVersionRequest 获取版本请求
type GetVersionRequest struct {
	Request
	ChunkID string `json:"chunk_id"`
}

// GetVersionResponse 获取版本响应
type GetVersionResponse struct {
	Response
	Version     int64             `json:"version"`
	VectorClock map[string]uint64 `json:"vector_clock"`
	Timestamp   int64             `json:"timestamp"`
	Checksum    string            `json:"checksum"`
	IsPrimary   bool              `json:"is_primary"`
}

// ===========================================
// 一致性检查相关消息
// ===========================================

// CheckConsistencyRequest 一致性检查请求
type CheckConsistencyRequest struct {
	Request
	ChunkID         string            `json:"chunk_id"`
	ExpectedVersion int64             `json:"expected_version,omitempty"`
	VectorClock     map[string]uint64 `json:"vector_clock,omitempty"`
}

// CheckConsistencyResponse 一致性检查响应
type CheckConsistencyResponse struct {
	Response
	IsConsistent   bool              `json:"is_consistent"`
	CurrentVersion int64             `json:"current_version"`
	VectorClock    map[string]uint64 `json:"vector_clock"`
	NeedSync       bool              `json:"need_sync"`
}

// WriteMessage 写入消息到连接
func WriteMessage(w io.Writer, msg *Message) error {
	// 序列化payload
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// 写入长度（4字节）
	length := uint32(len(data))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return err
	}

	// 写入数据
	_, err = w.Write(data)
	return err
}

// ReadMessage 从连接读取消息
func ReadMessage(r io.Reader) (*Message, error) {
	// 读取长度
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	// 读取数据
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	// 反序列化
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}

	return &msg, nil
}

// EncodePayload 编码请求/响应到payload
func EncodePayload(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// DecodePayload 解码payload到请求/响应
func DecodePayload(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// Package raft 元数据状态机实现
package raft

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/lumeofs/lumeofs/pkg/common"
)

// MetadataStateMachine 元数据状态机
// 实现 StateMachine 接口，管理分布式文件系统的元数据
type MetadataStateMachine struct {
	mu sync.RWMutex

	// 文件元数据
	files map[common.FileID]*common.FileMetadata

	// 数据块信息
	chunks map[common.ChunkID]*common.ChunkInfo

	// 数据节点信息
	datanodes map[common.NodeID]*common.DataNodeInfo

	// 版本号（用于快照）
	version uint64
}

// NewMetadataStateMachine 创建元数据状态机
func NewMetadataStateMachine() *MetadataStateMachine {
	return &MetadataStateMachine{
		files:     make(map[common.FileID]*common.FileMetadata),
		chunks:    make(map[common.ChunkID]*common.ChunkInfo),
		datanodes: make(map[common.NodeID]*common.DataNodeInfo),
	}
}

// ===========================================
// StateMachine 接口实现
// ===========================================

// Apply 应用命令到状态机
func (sm *MetadataStateMachine) Apply(cmd *MetadataCommand) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.version++

	switch cmd.Type {
	case CmdRegisterNode:
		return sm.applyRegisterNode(cmd)
	case CmdUnregisterNode:
		return sm.applyUnregisterNode(cmd)
	case CmdCreateFile:
		return sm.applyCreateFile(cmd)
	case CmdDeleteFile:
		return sm.applyDeleteFile(cmd)
	case CmdAllocChunk:
		return sm.applyAllocChunk(cmd)
	case CmdUpdateChunk:
		return sm.applyUpdateChunk(cmd)
	default:
		return fmt.Errorf("unknown command type: %s", cmd.Type)
	}
}

// Snapshot 创建快照
func (sm *MetadataStateMachine) Snapshot() ([]byte, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	snapshot := &MetadataSnapshot{
		Version:   sm.version,
		Files:     sm.files,
		Chunks:    sm.chunks,
		DataNodes: sm.datanodes,
		Timestamp: time.Now().UnixNano(),
	}

	return json.Marshal(snapshot)
}

// Restore 从快照恢复
func (sm *MetadataStateMachine) Restore(data []byte) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var snapshot MetadataSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}

	sm.version = snapshot.Version
	sm.files = snapshot.Files
	sm.chunks = snapshot.Chunks
	sm.datanodes = snapshot.DataNodes

	log.Printf("[MetadataSM] 从快照恢复: version=%d, files=%d, chunks=%d, nodes=%d",
		sm.version, len(sm.files), len(sm.chunks), len(sm.datanodes))

	return nil
}

// ===========================================
// 快照数据结构
// ===========================================

// MetadataSnapshot 元数据快照
type MetadataSnapshot struct {
	Version   uint64                                 `json:"version"`
	Files     map[common.FileID]*common.FileMetadata `json:"files"`
	Chunks    map[common.ChunkID]*common.ChunkInfo   `json:"chunks"`
	DataNodes map[common.NodeID]*common.DataNodeInfo `json:"datanodes"`
	Timestamp int64                                  `json:"timestamp"`
}

// ===========================================
// 命令应用实现
// ===========================================

// RegisterNodeData 注册节点数据
type RegisterNodeData struct {
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	TotalSpace int64  `json:"total_space"`
	UsedSpace  int64  `json:"used_space"`
}

func (sm *MetadataStateMachine) applyRegisterNode(cmd *MetadataCommand) error {
	var data RegisterNodeData
	if err := json.Unmarshal(cmd.Value, &data); err != nil {
		return err
	}

	nodeID := common.NodeID(data.NodeID)
	sm.datanodes[nodeID] = &common.DataNodeInfo{
		ID:            nodeID,
		Address:       data.Address,
		Port:          data.Port,
		TotalSpace:    data.TotalSpace,
		UsedSpace:     data.UsedSpace,
		Status:        common.NodeOnline,
		LastHeartbeat: time.Now(),
	}

	log.Printf("[MetadataSM] 注册节点: %s (%s:%d)", data.NodeID, data.Address, data.Port)
	return nil
}

func (sm *MetadataStateMachine) applyUnregisterNode(cmd *MetadataCommand) error {
	nodeID := common.NodeID(cmd.Key)
	if _, ok := sm.datanodes[nodeID]; ok {
		sm.datanodes[nodeID].Status = common.NodeOffline
		log.Printf("[MetadataSM] 注销节点: %s", nodeID)
	}
	return nil
}

// CreateFileData 创建文件数据
type CreateFileData struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
	Checksum string `json:"checksum"`
}

func (sm *MetadataStateMachine) applyCreateFile(cmd *MetadataCommand) error {
	var data CreateFileData
	if err := json.Unmarshal(cmd.Value, &data); err != nil {
		return err
	}

	fileID := common.FileID(data.FileID)
	sm.files[fileID] = &common.FileMetadata{
		ID:         fileID,
		Name:       data.FileName,
		Path:       data.FilePath,
		Size:       data.FileSize,
		ChunkIDs:   []common.ChunkID{},
		CreateTime: time.Now(),
		ModifyTime: time.Now(),
	}

	log.Printf("[MetadataSM] 创建文件: %s -> %s", data.FilePath, data.FileID)
	return nil
}

func (sm *MetadataStateMachine) applyDeleteFile(cmd *MetadataCommand) error {
	fileID := common.FileID(cmd.Key)
	if file, ok := sm.files[fileID]; ok {
		// 删除关联的数据块
		for _, chunkID := range file.ChunkIDs {
			delete(sm.chunks, chunkID)
		}
		delete(sm.files, fileID)
		log.Printf("[MetadataSM] 删除文件: %s", fileID)
	}
	return nil
}

// AllocChunkData 分配数据块数据
type AllocChunkData struct {
	ChunkID  string   `json:"chunk_id"`
	FileID   string   `json:"file_id"`
	Index    int      `json:"index"`
	Size     int64    `json:"size"`
	Replicas []string `json:"replicas"` // 节点 ID 列表
}

func (sm *MetadataStateMachine) applyAllocChunk(cmd *MetadataCommand) error {
	var data AllocChunkData
	if err := json.Unmarshal(cmd.Value, &data); err != nil {
		return err
	}

	chunkID := common.ChunkID(data.ChunkID)
	fileID := common.FileID(data.FileID)

	// 创建数据块信息
	chunk := &common.ChunkInfo{
		ID:         chunkID,
		FileID:     fileID,
		Index:      data.Index,
		Size:       data.Size,
		CreateTime: time.Now(),
		Replicas:   make([]common.ReplicaInfo, 0),
	}

	// 添加副本信息
	for i, nodeID := range data.Replicas {
		role := common.RoleSecondary
		if i == 0 {
			role = common.RolePrimary
		}
		chunk.Replicas = append(chunk.Replicas, common.ReplicaInfo{
			ChunkID:    chunkID,
			NodeID:     common.NodeID(nodeID),
			Role:       role,
			Status:     common.ReplicaHealthy,
			UpdateTime: time.Now(),
		})
	}

	sm.chunks[chunkID] = chunk

	// 更新文件的 ChunkIDs
	if file, ok := sm.files[fileID]; ok {
		file.ChunkIDs = append(file.ChunkIDs, chunkID)
	}

	log.Printf("[MetadataSM] 分配数据块: %s, 副本数: %d", data.ChunkID, len(data.Replicas))
	return nil
}

// UpdateChunkData 更新数据块数据
type UpdateChunkData struct {
	ChunkID  string `json:"chunk_id"`
	Version  int64  `json:"version"`
	Checksum string `json:"checksum"`
	Size     int64  `json:"size"`
}

func (sm *MetadataStateMachine) applyUpdateChunk(cmd *MetadataCommand) error {
	var data UpdateChunkData
	if err := json.Unmarshal(cmd.Value, &data); err != nil {
		return err
	}

	chunkID := common.ChunkID(data.ChunkID)
	if chunk, ok := sm.chunks[chunkID]; ok {
		chunk.Checksum = data.Checksum
		chunk.Size = data.Size
		log.Printf("[MetadataSM] 更新数据块: %s, version=%d", data.ChunkID, data.Version)
	}
	return nil
}

// ===========================================
// 查询接口
// ===========================================

// GetFile 获取文件信息
func (sm *MetadataStateMachine) GetFile(fileID common.FileID) *common.FileMetadata {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.files[fileID]
}

// GetFileByPath 按路径获取文件
func (sm *MetadataStateMachine) GetFileByPath(path string) *common.FileMetadata {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	for _, f := range sm.files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

// GetChunk 获取数据块信息
func (sm *MetadataStateMachine) GetChunk(chunkID common.ChunkID) *common.ChunkInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.chunks[chunkID]
}

// GetDataNode 获取数据节点信息
func (sm *MetadataStateMachine) GetDataNode(nodeID common.NodeID) *common.DataNodeInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.datanodes[nodeID]
}

// GetOnlineNodes 获取在线节点列表
func (sm *MetadataStateMachine) GetOnlineNodes() []*common.DataNodeInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var nodes []*common.DataNodeInfo
	for _, node := range sm.datanodes {
		if node.Status == common.NodeOnline {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// GetStats 获取统计信息
func (sm *MetadataStateMachine) GetStats() (fileCount, chunkCount, nodeCount int) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	onlineNodes := 0
	for _, node := range sm.datanodes {
		if node.Status == common.NodeOnline {
			onlineNodes++
		}
	}

	return len(sm.files), len(sm.chunks), onlineNodes
}

// ListFiles 列出所有文件
func (sm *MetadataStateMachine) ListFiles() []*common.FileMetadata {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var files []*common.FileMetadata
	for _, f := range sm.files {
		files = append(files, f)
	}
	return files
}

// ListDataNodes 列出所有数据节点
func (sm *MetadataStateMachine) ListDataNodes() []*common.DataNodeInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var nodes []*common.DataNodeInfo
	for _, n := range sm.datanodes {
		nodes = append(nodes, n)
	}
	return nodes
}

// ===========================================
// 更新接口（直接更新，用于心跳等不需要复制的操作）
// ===========================================

// UpdateNodeHeartbeat 更新节点心跳（不需要Raft复制）
func (sm *MetadataStateMachine) UpdateNodeHeartbeat(nodeID common.NodeID, chunkCount int, usedSpace int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if node, ok := sm.datanodes[nodeID]; ok {
		node.LastHeartbeat = time.Now()
		node.ChunkCount = chunkCount
		node.UsedSpace = usedSpace
		if node.Status == common.NodeOffline {
			node.Status = common.NodeOnline
			log.Printf("[MetadataSM] 节点重新上线: %s", nodeID)
		}
	}
}

// SetNodeOffline 设置节点离线
func (sm *MetadataStateMachine) SetNodeOffline(nodeID common.NodeID) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if node, ok := sm.datanodes[nodeID]; ok {
		node.Status = common.NodeOffline
		log.Printf("[MetadataSM] 节点离线: %s", nodeID)
	}
}

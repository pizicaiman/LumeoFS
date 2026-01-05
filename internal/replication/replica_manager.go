// Package replication 实现副本管理和数据同步
package replication

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/lumeofs/lumeofs/internal/version"
	"github.com/lumeofs/lumeofs/pkg/protocol"
)

// ReplicaRole 副本角色
type ReplicaRole int

const (
	RolePrimary   ReplicaRole = iota // 主副本：负责写入
	RoleSecondary                    // 从副本：只读，同步数据
)

func (r ReplicaRole) String() string {
	switch r {
	case RolePrimary:
		return "PRIMARY"
	case RoleSecondary:
		return "SECONDARY"
	default:
		return "UNKNOWN"
	}
}

// ReplicaInfo 副本信息
type ReplicaInfo struct {
	ChunkID     string
	NodeID      string
	NodeAddress string
	Role        ReplicaRole
	Version     *version.DataVersion
	LastSync    time.Time
	IsHealthy   bool
}

// ReplicaManager 副本管理器
type ReplicaManager struct {
	mu sync.RWMutex

	// 本节点信息
	localNodeID  string
	localAddress string

	// 数据块的副本信息 chunkID -> []ReplicaInfo
	replicas map[string][]*ReplicaInfo

	// 本节点持有的数据块角色 chunkID -> Role
	localRoles map[string]ReplicaRole

	// 一致性检查器
	consistencyChecker *version.ConsistencyChecker

	// 写代理连接池
	proxyConns map[string]net.Conn
}

// NewReplicaManager 创建副本管理器
func NewReplicaManager(nodeID, address string) *ReplicaManager {
	return &ReplicaManager{
		localNodeID:  nodeID,
		localAddress: address,
		replicas:     make(map[string][]*ReplicaInfo),
		localRoles:   make(map[string]ReplicaRole),
		proxyConns:   make(map[string]net.Conn),
		consistencyChecker: version.NewConsistencyChecker(
			5*time.Second,  // 最大时钟偏差
			30*time.Second, // 租约时长
		),
	}
}

// SetLocalRole 设置本地节点对某个数据块的角色
func (rm *ReplicaManager) SetLocalRole(chunkID string, role ReplicaRole) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.localRoles[chunkID] = role
	log.Printf("[ReplicaManager] 设置数据块 %s 角色为 %s", chunkID, role)
}

// GetLocalRole 获取本地节点对某个数据块的角色
func (rm *ReplicaManager) GetLocalRole(chunkID string) ReplicaRole {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	if role, ok := rm.localRoles[chunkID]; ok {
		return role
	}
	return RoleSecondary // 默认为从副本
}

// IsPrimary 检查本地是否是主副本
func (rm *ReplicaManager) IsPrimary(chunkID string) bool {
	return rm.GetLocalRole(chunkID) == RolePrimary
}

// RegisterReplicas 注册数据块的所有副本
func (rm *ReplicaManager) RegisterReplicas(chunkID string, replicas []*ReplicaInfo) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.replicas[chunkID] = replicas
}

// GetPrimaryNode 获取主副本节点地址
func (rm *ReplicaManager) GetPrimaryNode(chunkID string) (string, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if reps, ok := rm.replicas[chunkID]; ok {
		for _, rep := range reps {
			if rep.Role == RolePrimary {
				return rep.NodeAddress, nil
			}
		}
	}
	return "", fmt.Errorf("no primary replica found for chunk %s", chunkID)
}

// GetSecondaryNodes 获取所有从副本节点地址
func (rm *ReplicaManager) GetSecondaryNodes(chunkID string) []string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var secondaries []string
	if reps, ok := rm.replicas[chunkID]; ok {
		for _, rep := range reps {
			if rep.Role == RoleSecondary && rep.NodeID != rm.localNodeID {
				secondaries = append(secondaries, rep.NodeAddress)
			}
		}
	}
	return secondaries
}

// GetAllReplicas 获取所有副本信息
func (rm *ReplicaManager) GetAllReplicas(chunkID string) []*ReplicaInfo {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if reps, ok := rm.replicas[chunkID]; ok {
		return reps
	}
	return nil
}

// ===========================================
// 写代理机制
// ===========================================

// WriteResult 写入结果
type WriteResult struct {
	Success  bool
	Version  int64
	Error    string
	Checksum string
}

// Write 写入数据（带代理支持）
func (rm *ReplicaManager) Write(
	chunkID string,
	data []byte,
	offset int64,
	localWriteFunc func(chunkID string, data []byte, offset int64, checksum string) (*WriteResult, error),
) (*WriteResult, error) {
	// 计算校验和
	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:])

	// 检查是否是主副本
	if rm.IsPrimary(chunkID) {
		// 主副本直接写入
		log.Printf("[ReplicaManager] 主副本直接写入: %s", chunkID)
		return localWriteFunc(chunkID, data, offset, checksum)
	}

	// 从副本通过代理转发到主副本
	log.Printf("[ReplicaManager] 从副本代理写入: %s", chunkID)
	return rm.proxyWriteToPrimary(chunkID, data, offset, checksum)
}

// proxyWriteToPrimary 代理写入到主副本
func (rm *ReplicaManager) proxyWriteToPrimary(
	chunkID string,
	data []byte,
	offset int64,
	checksum string,
) (*WriteResult, error) {
	// 获取主副本地址
	primaryAddr, err := rm.GetPrimaryNode(chunkID)
	if err != nil {
		return nil, fmt.Errorf("无法获取主副本地址: %w", err)
	}

	// 处理0.0.0.0地址
	if len(primaryAddr) > 7 && primaryAddr[:7] == "0.0.0.0" {
		primaryAddr = "127.0.0.1" + primaryAddr[7:]
	}

	log.Printf("[ReplicaManager] 代理转发到主副本: %s", primaryAddr)

	// 连接主副本
	conn, err := net.DialTimeout("tcp", primaryAddr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("连接主副本失败: %w", err)
	}
	defer conn.Close()

	// 发送写请求
	req := protocol.WriteChunkRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("proxy_%d", time.Now().UnixNano()),
		},
		ChunkID:  chunkID,
		Data:     data,
		Offset:   offset,
		Checksum: checksum,
	}

	payload, _ := protocol.EncodePayload(req)
	if err := protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeWriteChunk,
		Payload: payload,
	}); err != nil {
		return nil, fmt.Errorf("发送写请求失败: %w", err)
	}

	// 读取响应
	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var resp protocol.WriteChunkResponse
	if err := protocol.DecodePayload(respMsg.Payload, &resp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return &WriteResult{
		Success:  resp.Success,
		Version:  resp.Version,
		Error:    resp.Error,
		Checksum: checksum,
	}, nil
}

// ===========================================
// 读取一致性检查
// ===========================================

// ReadResult 读取结果
type ReadResult struct {
	Data        []byte
	Version     *version.DataVersion
	Checksum    string
	FromPrimary bool
	Error       string
}

// Read 读取数据（带一致性检查）
func (rm *ReplicaManager) Read(
	chunkID string,
	offset int64,
	length int64,
	localVersion *version.DataVersion,
	localReadFunc func(chunkID string, offset, length int64) (*ReadResult, error),
) (*ReadResult, error) {
	isPrimary := rm.IsPrimary(chunkID)

	// 使用一致性检查器检查是否可以本地读取
	canRead, needPrimary, err := rm.consistencyChecker.CheckReadConsistency(
		chunkID, localVersion, rm.localNodeID, isPrimary,
	)

	if err != nil {
		return nil, err
	}

	if canRead && !needPrimary {
		// 可以本地读取
		log.Printf("[ReplicaManager] 本地读取: %s (isPrimary=%v)", chunkID, isPrimary)
		result, err := localReadFunc(chunkID, offset, length)
		if err != nil {
			return nil, err
		}
		result.FromPrimary = isPrimary
		return result, nil
	}

	// 需要从主副本读取
	log.Printf("[ReplicaManager] 需要从主副本读取: %s", chunkID)
	return rm.readFromPrimary(chunkID, offset, length)
}

// readFromPrimary 从主副本读取
func (rm *ReplicaManager) readFromPrimary(
	chunkID string,
	offset int64,
	length int64,
) (*ReadResult, error) {
	primaryAddr, err := rm.GetPrimaryNode(chunkID)
	if err != nil {
		return nil, err
	}

	// 处理0.0.0.0地址
	if len(primaryAddr) > 7 && primaryAddr[:7] == "0.0.0.0" {
		primaryAddr = "127.0.0.1" + primaryAddr[7:]
	}

	conn, err := net.DialTimeout("tcp", primaryAddr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("连接主副本失败: %w", err)
	}
	defer conn.Close()

	req := protocol.ReadChunkRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("read_%d", time.Now().UnixNano()),
		},
		ChunkID: chunkID,
		Offset:  offset,
		Length:  length,
	}

	payload, _ := protocol.EncodePayload(req)
	if err := protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeReadChunk,
		Payload: payload,
	}); err != nil {
		return nil, err
	}

	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		return nil, err
	}

	var resp protocol.ReadChunkResponse
	if err := protocol.DecodePayload(respMsg.Payload, &resp); err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("主副本读取失败: %s", resp.Error)
	}

	return &ReadResult{
		Data:        resp.Data,
		Checksum:    resp.Checksum,
		FromPrimary: true,
	}, nil
}

// ===========================================
// 副本同步
// ===========================================

// SyncFromPrimary 从主副本同步数据
func (rm *ReplicaManager) SyncFromPrimary(
	chunkID string,
	localWriteFunc func(chunkID string, data []byte, offset int64, checksum string) error,
) error {
	if rm.IsPrimary(chunkID) {
		return nil // 主副本不需要同步
	}

	// 从主副本读取完整数据
	result, err := rm.readFromPrimary(chunkID, 0, 0)
	if err != nil {
		return fmt.Errorf("从主副本读取失败: %w", err)
	}

	// 写入本地
	if err := localWriteFunc(chunkID, result.Data, 0, result.Checksum); err != nil {
		return fmt.Errorf("本地写入失败: %w", err)
	}

	log.Printf("[ReplicaManager] 从主副本同步完成: %s, size=%d", chunkID, len(result.Data))
	return nil
}

// ===========================================
// 版本一致性验证
// ===========================================

// VersionInfo 版本信息（用于一致性检查）
type VersionInfo struct {
	ChunkID  string
	NodeID   string
	Version  *version.DataVersion
	Checksum string
}

// ValidateConsistency 验证副本一致性
func (rm *ReplicaManager) ValidateConsistency(chunkID string, versions []VersionInfo) (bool, string) {
	if len(versions) == 0 {
		return false, "no versions to validate"
	}

	// 检查所有版本号是否一致
	firstVersion := versions[0].Version
	firstChecksum := versions[0].Checksum

	for _, v := range versions[1:] {
		if v.Version != nil && firstVersion != nil {
			if !v.Version.IsConsistentWith(firstVersion) {
				return false, fmt.Sprintf("version mismatch: %s vs %s",
					v.NodeID, versions[0].NodeID)
			}
		}
		if v.Checksum != firstChecksum {
			return false, fmt.Sprintf("checksum mismatch: %s vs %s",
				v.NodeID, versions[0].NodeID)
		}
	}

	return true, ""
}

// GetConsistencyChecker 获取一致性检查器
func (rm *ReplicaManager) GetConsistencyChecker() *version.ConsistencyChecker {
	return rm.consistencyChecker
}

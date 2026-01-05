// Package datanode 实现LumeoFS数据节点的核心逻辑
package datanode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lumeofs/lumeofs/internal/replication"
	"github.com/lumeofs/lumeofs/internal/version"
	"github.com/lumeofs/lumeofs/internal/wal"
	"github.com/lumeofs/lumeofs/pkg/common"
	"github.com/lumeofs/lumeofs/pkg/config"
	"github.com/lumeofs/lumeofs/pkg/protocol"
)

// DataNode 数据节点服务
type DataNode struct {
	config config.DataNodeConfig
	nodeID common.NodeID
	mu     sync.RWMutex

	// 本地存储
	storage *LocalStorage

	// WAL 日志
	walLog *wal.WAL

	// 副本数据
	chunks map[common.ChunkID]*LocalChunk

	// 副本管理器
	replicaManager *replication.ReplicaManager

	// 服务状态
	running  bool
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
}

// LocalChunk 本地数据块信息
type LocalChunk struct {
	ID        common.ChunkID
	Role      common.ReplicaRole
	Version   *version.DataVersion // 版本信息
	Size      int64
	Checksum  string
	FilePath  string
	IsPrimary bool // 是否是主副本
}

// NewDataNode 创建数据节点实例
func NewDataNode(cfg config.DataNodeConfig) (*DataNode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 生成节点ID
	nodeID := cfg.NodeID
	if nodeID == "" {
		nodeID = fmt.Sprintf("node_%d", time.Now().UnixNano())
	}

	// 初始化WAL
	walLog, err := wal.NewWAL(cfg.WALDir)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize WAL: %w", err)
	}

	// 初始化存储
	storage, err := NewLocalStorage(cfg.DataDir)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	dn := &DataNode{
		config:  cfg,
		nodeID:  common.NodeID(nodeID),
		chunks:  make(map[common.ChunkID]*LocalChunk),
		walLog:  walLog,
		storage: storage,
		ctx:     ctx,
		cancel:  cancel,
	}

	// 初始化副本管理器
	addr := fmt.Sprintf("%s:%d", cfg.Address, cfg.Port)
	dn.replicaManager = replication.NewReplicaManager(nodeID, addr)

	return dn, nil
}

// Start 启动数据节点服务
func (dn *DataNode) Start() error {
	addr := fmt.Sprintf("%s:%d", dn.config.Address, dn.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	dn.listener = listener
	dn.running = true

	log.Printf("[DataNode] 启动成功，节点ID: %s", dn.nodeID)
	log.Printf("[DataNode] 监听地址: %s", addr)

	// 恢复WAL日志
	if err := dn.recoverFromWAL(); err != nil {
		log.Printf("[DataNode] WAL恢复警告: %v", err)
	}

	// 注册到主节点
	if err := dn.registerToMaster(); err != nil {
		log.Printf("[DataNode] 注册到主节点失败: %v", err)
	} else {
		log.Printf("[DataNode] 注册到主节点成功")
	}

	// 启动心跳上报
	go dn.heartbeatReporter()

	// 启动连接处理
	go dn.acceptConnections()

	return nil
}

// Stop 停止数据节点服务
func (dn *DataNode) Stop() error {
	dn.cancel()
	dn.running = false

	if dn.walLog != nil {
		dn.walLog.Close()
	}

	if dn.listener != nil {
		return dn.listener.Close()
	}
	return nil
}

// acceptConnections 处理客户端连接
func (dn *DataNode) acceptConnections() {
	for dn.running {
		conn, err := dn.listener.Accept()
		if err != nil {
			if dn.running {
				log.Printf("[DataNode] 接受连接失败: %v", err)
			}
			continue
		}
		go dn.handleConnection(conn)
	}
}

// handleConnection 处理单个连接
func (dn *DataNode) handleConnection(conn net.Conn) {
	defer conn.Close()
	log.Printf("[DataNode] 新连接来自: %s", conn.RemoteAddr())

	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			return
		}

		switch msg.Type {
		case protocol.MsgTypeWriteChunk:
			dn.handleWriteChunk(conn, msg)
		case protocol.MsgTypeReadChunk:
			dn.handleReadChunk(conn, msg)
		case protocol.MsgTypeSyncChunk:
			dn.handleSyncChunk(conn, msg)
		case protocol.MsgTypeSetRole:
			dn.handleSetRole(conn, msg)
		case protocol.MsgTypeGetVersion:
			dn.handleGetVersion(conn, msg)
		default:
			log.Printf("[DataNode] 未知消息类型: %d", msg.Type)
		}
	}
}

// heartbeatReporter 心跳上报协程
func (dn *DataNode) heartbeatReporter() {
	ticker := time.NewTicker(dn.config.HeartbeatPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-dn.ctx.Done():
			return
		case <-ticker.C:
			dn.sendHeartbeat()
		}
	}
}

// sendHeartbeat 发送心跳到主节点
func (dn *DataNode) sendHeartbeat() {
	masterAddr := fmt.Sprintf("%s:%d", dn.config.MasterAddress, dn.config.MasterPort)
	conn, err := net.DialTimeout("tcp", masterAddr, 3*time.Second)
	if err != nil {
		log.Printf("[DataNode] 心跳连接失败: %v", err)
		return
	}
	defer conn.Close()

	dn.mu.RLock()
	chunkCount := len(dn.chunks)
	dn.mu.RUnlock()

	req := protocol.HeartbeatRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("hb_%d", time.Now().UnixNano()),
		},
		NodeID:     string(dn.nodeID),
		ChunkCount: chunkCount,
	}

	payload, _ := protocol.EncodePayload(req)
	if err := protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeHeartbeat,
		Payload: payload,
	}); err != nil {
		log.Printf("[DataNode] 发送心跳失败: %v", err)
		return
	}

	// 读取响应
	_, err = protocol.ReadMessage(conn)
	if err != nil {
		log.Printf("[DataNode] 心跳响应失败: %v", err)
	}
}

// registerToMaster 注册到主节点
func (dn *DataNode) registerToMaster() error {
	masterAddr := fmt.Sprintf("%s:%d", dn.config.MasterAddress, dn.config.MasterPort)
	conn, err := net.DialTimeout("tcp", masterAddr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("连接主节点失败: %w", err)
	}
	defer conn.Close()

	req := protocol.RegisterRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("reg_%d", time.Now().UnixNano()),
		},
		NodeID:  string(dn.nodeID),
		Address: dn.config.Address,
		Port:    dn.config.Port,
	}

	payload, _ := protocol.EncodePayload(req)
	if err := protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeRegister,
		Payload: payload,
	}); err != nil {
		return fmt.Errorf("发送注册请求失败: %w", err)
	}

	// 读取响应
	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("读取注册响应失败: %w", err)
	}

	var resp protocol.RegisterResponse
	if err := protocol.DecodePayload(respMsg.Payload, &resp); err != nil {
		return fmt.Errorf("解析注册响应失败: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("注册失败: %s", resp.Error)
	}

	return nil
}

// handleWriteChunk 处理写数据块请求
func (dn *DataNode) handleWriteChunk(conn net.Conn, msg *protocol.Message) {
	var req protocol.WriteChunkRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[DataNode] 解析写请求失败: %v", err)
		return
	}

	log.Printf("[DataNode] 收到写请求: ChunkID=%s, Size=%d", req.ChunkID, len(req.Data))

	chunkID := common.ChunkID(req.ChunkID)

	// 检查是否已设置角色，如果没有则默认为主副本（首次写入）
	role := dn.replicaManager.GetLocalRole(req.ChunkID)
	// 如果数据块不存在，这是首次写入，默认为主副本
	dn.mu.RLock()
	_, chunkExists := dn.chunks[chunkID]
	dn.mu.RUnlock()

	if !chunkExists {
		// 首次写入，设置为主副本
		dn.replicaManager.SetLocalRole(req.ChunkID, replication.RolePrimary)
		role = replication.RolePrimary
		log.Printf("[DataNode] 首次写入数据块 %s，设置为主副本", req.ChunkID)
	}

	isPrimary := (role == replication.RolePrimary)

	if !isPrimary {
		// 非主副本，通过代理转发到主副本
		log.Printf("[DataNode] 非主副本，代理写请求: %s", req.ChunkID)
		result, err := dn.replicaManager.Write(
			req.ChunkID,
			req.Data,
			req.Offset,
			func(cID string, data []byte, offset int64, checksum string) (*replication.WriteResult, error) {
				// 此回调不会被调用，因为是代理模式
				return nil, fmt.Errorf("should not reach here")
			},
		)

		resp := protocol.WriteChunkResponse{
			Response: protocol.Response{
				RequestID: req.RequestID,
			},
		}

		if err != nil {
			resp.Success = false
			resp.Error = err.Error()
		} else {
			resp.Success = result.Success
			resp.Version = result.Version
			resp.Error = result.Error
		}

		payload, _ := protocol.EncodePayload(resp)
		protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
		return
	}

	// 主副本本地写入
	log.Printf("[DataNode] 主副本本地写入: %s", req.ChunkID)

	// 写WAL
	walEntry := &wal.Entry{
		Type:     wal.EntryTypeWrite,
		ChunkID:  req.ChunkID,
		Data:     req.Data,
		Offset:   req.Offset,
		Checksum: req.Checksum,
	}
	if err := dn.walLog.Append(walEntry); err != nil {
		log.Printf("[DataNode] WAL写入失败: %v", err)
	}

	// 写入存储
	if err := dn.storage.WriteChunk(chunkID, req.Data, req.Offset); err != nil {
		log.Printf("[DataNode] 存储写入失败: %v", err)
		resp := protocol.WriteChunkResponse{
			Response: protocol.Response{
				RequestID: req.RequestID,
				Success:   false,
				Error:     err.Error(),
			},
		}
		payload, _ := protocol.EncodePayload(resp)
		protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
		return
	}

	// 更新内存记录
	dn.mu.Lock()
	if _, exists := dn.chunks[chunkID]; !exists {
		dn.chunks[chunkID] = &LocalChunk{
			ID:        chunkID,
			Version:   version.NewDataVersion(string(dn.nodeID)),
			IsPrimary: true,
		}
	}
	dn.chunks[chunkID].Size = int64(len(req.Data))
	dn.chunks[chunkID].Version.Increment(string(dn.nodeID))
	dn.chunks[chunkID].Checksum = req.Checksum
	logicalVersion := dn.chunks[chunkID].Version.LogicalVersion
	chunk := dn.chunks[chunkID]
	dn.mu.Unlock()

	log.Printf("[DataNode] 主副本写入成功: %s, version=%d", req.ChunkID, logicalVersion)

	// 异步同步到从副本
	go dn.syncToSecondariesAsync(req.ChunkID, req.Data, chunk.Version)

	resp := protocol.WriteChunkResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
		Version: logicalVersion,
	}
	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
}

// handleReadChunk 处理读数据块请求
func (dn *DataNode) handleReadChunk(conn net.Conn, msg *protocol.Message) {
	var req protocol.ReadChunkRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[DataNode] 解析读请求失败: %v", err)
		return
	}

	log.Printf("[DataNode] 收到读请求: ChunkID=%s", req.ChunkID)

	chunkID := common.ChunkID(req.ChunkID)
	length := req.Length
	if length == 0 {
		length = 64 * 1024 * 1024 // 默认读全部
	}

	// 检查一致性（从副本需要检查）
	isPrimary := dn.replicaManager.IsPrimary(req.ChunkID)
	dn.mu.RLock()
	localChunk := dn.chunks[chunkID]
	var localVersion *version.DataVersion
	if localChunk != nil {
		localVersion = localChunk.Version
	}
	dn.mu.RUnlock()

	// 使用副本管理器的一致性检查
	result, err := dn.replicaManager.Read(
		req.ChunkID,
		req.Offset,
		length,
		localVersion,
		func(cID string, offset, len int64) (*replication.ReadResult, error) {
			// 本地读取
			data, err := dn.storage.ReadChunk(common.ChunkID(cID), offset, len)
			if err != nil {
				return nil, err
			}
			return &replication.ReadResult{
				Data:     data,
				Version:  localVersion,
				Checksum: dn.calculateChecksum(data),
			}, nil
		},
	)

	if err != nil {
		resp := protocol.ReadChunkResponse{
			Response: protocol.Response{
				RequestID: req.RequestID,
				Success:   false,
				Error:     err.Error(),
			},
		}
		payload, _ := protocol.EncodePayload(resp)
		protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
		return
	}

	var logicalVer int64
	if result.Version != nil {
		logicalVer = result.Version.LogicalVersion
	}

	resp := protocol.ReadChunkResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
		Data:     result.Data,
		Checksum: result.Checksum,
		Version:  logicalVer,
	}
	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
	log.Printf("[DataNode] 数据块读取成功: %s, size=%d, isPrimary=%v, fromPrimary=%v",
		req.ChunkID, len(result.Data), isPrimary, result.FromPrimary)
}

// handleSyncChunk 处理副本同步请求
func (dn *DataNode) handleSyncChunk(conn net.Conn, msg *protocol.Message) {
	var req protocol.SyncChunkRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[DataNode] 解析同步请求失败: %v", err)
		return
	}

	log.Printf("[DataNode] 收到副本同步: ChunkID=%s, version=%d, from=%s",
		req.ChunkID, req.Version, req.SourceNodeID)

	chunkID := common.ChunkID(req.ChunkID)

	// 写入存储
	if err := dn.storage.WriteChunk(chunkID, req.Data, req.Offset); err != nil {
		log.Printf("[DataNode] 同步写入失败: %v", err)
		resp := protocol.SyncChunkResponse{
			Response: protocol.Response{
				RequestID: req.RequestID,
				Success:   false,
				Error:     err.Error(),
			},
		}
		payload, _ := protocol.EncodePayload(resp)
		protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
		return
	}

	// 更新内存记录
	dn.mu.Lock()
	if _, exists := dn.chunks[chunkID]; !exists {
		dn.chunks[chunkID] = &LocalChunk{
			ID:        chunkID,
			Version:   version.NewDataVersion(string(dn.nodeID)),
			IsPrimary: false,
		}
	}
	// 更新版本信息
	dn.chunks[chunkID].Version.LogicalVersion = req.Version
	dn.chunks[chunkID].Version.VectorClock = req.VectorClock
	dn.chunks[chunkID].Version.Timestamp = req.Timestamp
	dn.chunks[chunkID].Size = int64(len(req.Data))
	dn.chunks[chunkID].Checksum = req.Checksum
	dn.mu.Unlock()

	log.Printf("[DataNode] 副本同步成功: %s, version=%d", req.ChunkID, req.Version)

	resp := protocol.SyncChunkResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
		Version: req.Version,
	}
	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
}

// handleSetRole 处理角色设置请求
func (dn *DataNode) handleSetRole(conn net.Conn, msg *protocol.Message) {
	var req protocol.SetRoleRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[DataNode] 解析角色设置请求失败: %v", err)
		return
	}

	log.Printf("[DataNode] 收到角色设置: ChunkID=%s, Role=%s", req.ChunkID, req.Role)

	// 设置本地角色
	var role replication.ReplicaRole
	if req.Role == "PRIMARY" {
		role = replication.RolePrimary
	} else {
		role = replication.RoleSecondary
	}
	dn.replicaManager.SetLocalRole(req.ChunkID, role)

	// 注册副本信息
	if len(req.Replicas) > 0 {
		replicas := make([]*replication.ReplicaInfo, len(req.Replicas))
		for i, r := range req.Replicas {
			repRole := replication.RoleSecondary
			if r.Role == "PRIMARY" {
				repRole = replication.RolePrimary
			}
			replicas[i] = &replication.ReplicaInfo{
				ChunkID:     req.ChunkID,
				NodeID:      r.NodeID,
				NodeAddress: r.Address,
				Role:        repRole,
				IsHealthy:   true,
			}
		}
		dn.replicaManager.RegisterReplicas(req.ChunkID, replicas)
	}

	// 更新本地数据块信息
	chunkID := common.ChunkID(req.ChunkID)
	dn.mu.Lock()
	if chunk, exists := dn.chunks[chunkID]; exists {
		chunk.IsPrimary = (req.Role == "PRIMARY")
		chunk.Role = common.ReplicaRole(role)
	}
	dn.mu.Unlock()

	resp := protocol.SetRoleResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
	}
	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
}

// handleGetVersion 处理版本查询请求
func (dn *DataNode) handleGetVersion(conn net.Conn, msg *protocol.Message) {
	var req protocol.GetVersionRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[DataNode] 解析版本查询请求失败: %v", err)
		return
	}

	chunkID := common.ChunkID(req.ChunkID)

	dn.mu.RLock()
	chunk, exists := dn.chunks[chunkID]
	dn.mu.RUnlock()

	resp := protocol.GetVersionResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
		},
	}

	if !exists {
		resp.Success = false
		resp.Error = "chunk not found"
	} else {
		resp.Success = true
		if chunk.Version != nil {
			resp.Version = chunk.Version.LogicalVersion
			resp.VectorClock = chunk.Version.VectorClock
			resp.Timestamp = chunk.Version.Timestamp
		}
		resp.Checksum = chunk.Checksum
		resp.IsPrimary = dn.replicaManager.IsPrimary(req.ChunkID)
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeResponse, Payload: payload})
}

// Write 写入数据（主副本接口）
func (dn *DataNode) Write(req *common.WriteRequest) (*common.WriteResponse, error) {
	dn.mu.Lock()
	defer dn.mu.Unlock()

	chunk, exists := dn.chunks[req.ChunkID]
	if !exists {
		// 创建新数据块
		chunk = &LocalChunk{
			ID:        req.ChunkID,
			Role:      common.RolePrimary,
			Version:   version.NewDataVersion(string(dn.nodeID)),
			IsPrimary: true,
		}
		dn.chunks[req.ChunkID] = chunk
	}

	// 检查是否是主副本
	if chunk.Role != common.RolePrimary {
		return &common.WriteResponse{
			Success: false,
			Error:   "not primary replica, cannot write",
		}, nil
	}

	nextVersion := int64(1)
	if chunk.Version != nil {
		nextVersion = chunk.Version.LogicalVersion + 1
	}

	// 步骤1: 先写WAL日志
	walEntry := &wal.Entry{
		Type:     wal.EntryTypeWrite,
		ChunkID:  string(req.ChunkID),
		Data:     req.Data,
		Offset:   req.Offset,
		Version:  nextVersion,
		Checksum: req.Checksum,
	}

	if err := dn.walLog.Append(walEntry); err != nil {
		return &common.WriteResponse{
			Success: false,
			Error:   fmt.Sprintf("WAL write failed: %v", err),
		}, nil
	}

	// 步骤2: WAL写入成功后，刷盘到实际数据文件
	if err := dn.storage.WriteChunk(req.ChunkID, req.Data, req.Offset); err != nil {
		return &common.WriteResponse{
			Success: false,
			Error:   fmt.Sprintf("storage write failed: %v", err),
		}, nil
	}

	// 更新元数据
	if chunk.Version == nil {
		chunk.Version = version.NewDataVersion(string(dn.nodeID))
	} else {
		chunk.Version.Increment(string(dn.nodeID))
	}
	chunk.Size = int64(len(req.Data))
	chunk.Checksum = dn.calculateChecksum(req.Data)

	log.Printf("[DataNode] 数据写入成功: chunk=%s, version=%d", req.ChunkID, chunk.Version.LogicalVersion)

	// 步骤3: 同步到从副本
	go dn.syncToSecondaries(req.ChunkID, req.Data, chunk.Version.LogicalVersion)

	return &common.WriteResponse{
		Success: true,
		Version: chunk.Version.LogicalVersion,
	}, nil
}

// Read 读取数据
func (dn *DataNode) Read(req *common.ReadRequest) (*common.ReadResponse, error) {
	dn.mu.RLock()
	defer dn.mu.RUnlock()

	chunk, exists := dn.chunks[req.ChunkID]
	if !exists {
		return &common.ReadResponse{
			Error: "chunk not found",
		}, nil
	}

	data, err := dn.storage.ReadChunk(req.ChunkID, req.Offset, req.Length)
	if err != nil {
		return &common.ReadResponse{
			Error: fmt.Sprintf("read failed: %v", err),
		}, nil
	}

	var ver int64
	if chunk.Version != nil {
		ver = chunk.Version.LogicalVersion
	}

	return &common.ReadResponse{
		Data:     data,
		Checksum: dn.calculateChecksum(data),
		Version:  ver,
	}, nil
}

// syncToSecondaries 同步数据到从副本
func (dn *DataNode) syncToSecondaries(chunkID common.ChunkID, data []byte, ver int64) {
	// TODO: 实现与其他节点的数据同步
	log.Printf("[DataNode] 开始同步数据到从副本: chunk=%s, version=%d", chunkID, ver)
}

// syncToSecondariesAsync 异步同步数据到从副本
func (dn *DataNode) syncToSecondariesAsync(chunkID string, data []byte, ver *version.DataVersion) {
	secondaries := dn.replicaManager.GetSecondaryNodes(chunkID)
	if len(secondaries) == 0 {
		log.Printf("[DataNode] 没有从副本需要同步: %s", chunkID)
		return
	}

	log.Printf("[DataNode] 开始同步数据到 %d 个从副本: chunk=%s, version=%d",
		len(secondaries), chunkID, ver.LogicalVersion)

	checksum := dn.calculateChecksum(data)

	for _, nodeAddr := range secondaries {
		go func(addr string) {
			if err := dn.syncToNode(chunkID, data, ver, checksum, addr); err != nil {
				log.Printf("[DataNode] 同步到节点 %s 失败: %v", addr, err)
			} else {
				log.Printf("[DataNode] 同步到节点 %s 成功", addr)
			}
		}(nodeAddr)
	}
}

// syncToNode 同步数据到指定节点
func (dn *DataNode) syncToNode(chunkID string, data []byte, ver *version.DataVersion, checksum, nodeAddr string) error {
	// 处理 0.0.0.0 地址
	if len(nodeAddr) > 7 && nodeAddr[:7] == "0.0.0.0" {
		nodeAddr = "127.0.0.1" + nodeAddr[7:]
	}

	conn, err := net.DialTimeout("tcp", nodeAddr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("连接节点失败: %w", err)
	}
	defer conn.Close()

	req := protocol.SyncChunkRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("sync_%d", time.Now().UnixNano()),
		},
		ChunkID:      chunkID,
		Data:         data,
		Offset:       0,
		Checksum:     checksum,
		Version:      ver.LogicalVersion,
		VectorClock:  ver.VectorClock,
		Timestamp:    ver.Timestamp,
		SourceNodeID: string(dn.nodeID),
	}

	payload, _ := protocol.EncodePayload(req)
	if err := protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeSyncChunk,
		Payload: payload,
	}); err != nil {
		return fmt.Errorf("发送同步请求失败: %w", err)
	}

	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}

	var resp protocol.SyncChunkResponse
	if err := protocol.DecodePayload(respMsg.Payload, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("同步失败: %s", resp.Error)
	}

	return nil
}

// recoverFromWAL 从WAL日志恢复
func (dn *DataNode) recoverFromWAL() error {
	entries, err := dn.walLog.ReadAll()
	if err != nil {
		return err
	}

	log.Printf("[DataNode] 从WAL恢复 %d 条记录", len(entries))

	for _, entry := range entries {
		if entry.Type == wal.EntryTypeWrite {
			chunkID := common.ChunkID(entry.ChunkID)
			if err := dn.storage.WriteChunk(chunkID, entry.Data, entry.Offset); err != nil {
				log.Printf("[DataNode] WAL恢复写入失败: %v", err)
				continue
			}

			// 更新内存元数据
			dv := version.NewDataVersion(string(dn.nodeID))
			dv.LogicalVersion = entry.Version
			dn.chunks[chunkID] = &LocalChunk{
				ID:       chunkID,
				Version:  dv,
				Size:     int64(len(entry.Data)),
				Checksum: entry.Checksum,
			}
		}
	}

	return nil
}

// calculateChecksum 计算数据校验和
func (dn *DataNode) calculateChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// LocalStorage 本地存储实现
type LocalStorage struct {
	baseDir string
}

// NewLocalStorage 创建本地存储
func NewLocalStorage(baseDir string) (*LocalStorage, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, err
	}
	return &LocalStorage{baseDir: baseDir}, nil
}

// WriteChunk 写入数据块
func (ls *LocalStorage) WriteChunk(chunkID common.ChunkID, data []byte, offset int64) error {
	path := ls.chunkPath(chunkID)

	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}

	_, err = file.Write(data)
	return err
}

// ReadChunk 读取数据块
func (ls *LocalStorage) ReadChunk(chunkID common.ChunkID, offset, length int64) ([]byte, error) {
	path := ls.chunkPath(chunkID)

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}

	data := make([]byte, length)
	n, err := file.Read(data)
	if err != nil && err != io.EOF {
		return nil, err
	}

	return data[:n], nil
}

// chunkPath 获取数据块文件路径
func (ls *LocalStorage) chunkPath(chunkID common.ChunkID) string {
	return filepath.Join(ls.baseDir, "chunks", string(chunkID))
}

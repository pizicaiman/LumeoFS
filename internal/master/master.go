// Package master 实现LumeoFS主节点的核心逻辑
package master

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/lumeofs/lumeofs/pkg/common"
	"github.com/lumeofs/lumeofs/pkg/config"
	"github.com/lumeofs/lumeofs/pkg/protocol"
)

// Master 主节点服务
type Master struct {
	config config.MasterConfig
	mu     sync.RWMutex

	// 元数据管理
	files  map[common.FileID]*common.FileMetadata
	chunks map[common.ChunkID]*common.ChunkInfo

	// 节点管理
	datanodes map[common.NodeID]*common.DataNodeInfo

	// 副本管理
	replicaManager *ReplicaManager

	// 服务状态
	running  bool
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewMaster 创建主节点实例
func NewMaster(cfg config.MasterConfig) *Master {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Master{
		config:    cfg,
		files:     make(map[common.FileID]*common.FileMetadata),
		chunks:    make(map[common.ChunkID]*common.ChunkInfo),
		datanodes: make(map[common.NodeID]*common.DataNodeInfo),
		ctx:       ctx,
		cancel:    cancel,
	}
	m.replicaManager = NewReplicaManager(m, cfg.ReplicaCount)
	return m
}

// Start 启动主节点服务
func (m *Master) Start() error {
	addr := fmt.Sprintf("%s:%d", m.config.Address, m.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	m.listener = listener
	m.running = true

	log.Printf("[Master] 启动成功，监听地址: %s", addr)
	log.Printf("[Master] 副本数量配置: %d", m.config.ReplicaCount)

	// 启动心跳检测
	go m.heartbeatChecker()

	// 启动连接处理
	go m.acceptConnections()

	return nil
}

// Stop 停止主节点服务
func (m *Master) Stop() error {
	m.cancel()
	m.running = false
	if m.listener != nil {
		return m.listener.Close()
	}
	return nil
}

// acceptConnections 处理客户端连接
func (m *Master) acceptConnections() {
	for m.running {
		conn, err := m.listener.Accept()
		if err != nil {
			if m.running {
				log.Printf("[Master] 接受连接失败: %v", err)
			}
			continue
		}
		go m.handleConnection(conn)
	}
}

// handleConnection 处理单个连接
func (m *Master) handleConnection(conn net.Conn) {
	defer conn.Close()
	log.Printf("[Master] 新连接来自: %s", conn.RemoteAddr())

	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			log.Printf("[Master] 读取消息失败: %v", err)
			return
		}

		switch msg.Type {
		case protocol.MsgTypeRegister:
			m.handleRegister(conn, msg)
		case protocol.MsgTypeHeartbeat:
			m.handleHeartbeat(conn, msg)
		case protocol.MsgTypePutFile:
			m.handlePutFile(conn, msg)
		case protocol.MsgTypeGetFile:
			m.handleGetFile(conn, msg)
		case protocol.MsgTypeGetStatus:
			m.handleGetStatus(conn, msg)
		case protocol.MsgTypeListDir:
			m.handleListDir(conn, msg)
		case protocol.MsgTypeAllocChunk:
			m.handleAllocChunk(conn, msg)
		default:
			log.Printf("[Master] 未知消息类型: %d", msg.Type)
		}
	}
}

// handlePutFile 处理文件上传请求
func (m *Master) handlePutFile(conn net.Conn, msg *protocol.Message) {
	var req protocol.PutFileRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[Master] 解析上传请求失败: %v", err)
		return
	}

	log.Printf("[Master] 收到上传请求: %s, 大小: %d bytes", req.FileName, req.FileSize)

	// 生成文件ID
	fileID := common.FileID(fmt.Sprintf("file_%d", time.Now().UnixNano()))

	// 创建文件元数据
	m.mu.Lock()
	m.files[fileID] = &common.FileMetadata{
		ID:         fileID,
		Name:       req.FileName,
		Path:       req.FilePath,
		Size:       req.FileSize,
		ChunkIDs:   []common.ChunkID{},
		CreateTime: time.Now(),
		ModifyTime: time.Now(),
	}
	m.mu.Unlock()

	log.Printf("[Master] 文件已注册: %s -> %s", req.FilePath, fileID)

	// 发送响应
	resp := protocol.PutFileResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
		FileID: string(fileID),
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleGetFile 处理文件下载请求
func (m *Master) handleGetFile(conn net.Conn, msg *protocol.Message) {
	var req protocol.GetFileRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[Master] 解析下载请求失败: %v", err)
		return
	}

	log.Printf("[Master] 收到下载请求: %s", req.FilePath)

	// 查找文件
	m.mu.RLock()
	var foundFile *common.FileMetadata
	for _, f := range m.files {
		if f.Path == req.FilePath {
			foundFile = f
			break
		}
	}
	m.mu.RUnlock()

	resp := protocol.GetFileResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
		},
	}

	if foundFile != nil {
		resp.Success = true
		resp.FileName = foundFile.Name
		resp.FileSize = foundFile.Size
		log.Printf("[Master] 找到文件: %s", foundFile.Name)
	} else {
		resp.Success = false
		resp.Error = "file not found"
		log.Printf("[Master] 文件未找到: %s", req.FilePath)
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleGetStatus 处理状态查询请求
func (m *Master) handleGetStatus(conn net.Conn, msg *protocol.Message) {
	var req protocol.StatusRequest
	protocol.DecodePayload(msg.Payload, &req)

	log.Printf("[Master] 收到状态查询请求")

	m.mu.RLock()
	nodes := make([]protocol.NodeStatus, 0)
	for id, node := range m.datanodes {
		status := "offline"
		if node.Status == common.NodeOnline {
			status = "online"
		}
		nodes = append(nodes, protocol.NodeStatus{
			NodeID:     string(id),
			Address:    fmt.Sprintf("%s:%d", node.Address, node.Port),
			Status:     status,
			ChunkCount: node.ChunkCount,
		})
	}

	resp := protocol.StatusResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
		NodeCount:  len(m.datanodes),
		FileCount:  len(m.files),
		ChunkCount: len(m.chunks),
		Nodes:      nodes,
	}
	m.mu.RUnlock()

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleListDir 处理目录列表请求
func (m *Master) handleListDir(conn net.Conn, msg *protocol.Message) {
	type ListRequest struct {
		protocol.Request
		Path string `json:"path"`
	}
	type FileInfo struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		Path string `json:"path"`
	}
	type ListResponse struct {
		protocol.Response
		Files []FileInfo `json:"files"`
	}

	var req ListRequest
	protocol.DecodePayload(msg.Payload, &req)

	log.Printf("[Master] 收到列目录请求: %s", req.Path)

	m.mu.RLock()
	files := make([]FileInfo, 0)
	for _, f := range m.files {
		// 简单匹配路径前缀
		dir := filepath.Dir(f.Path)
		if dir == req.Path || req.Path == "/" {
			files = append(files, FileInfo{
				Name: f.Name,
				Size: f.Size,
				Path: f.Path,
			})
		}
	}
	m.mu.RUnlock()

	resp := ListResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
		Files: files,
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// generateChecksum 生成校验和
func generateChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// heartbeatChecker 心跳检测协程
func (m *Master) heartbeatChecker() {
	ticker := time.NewTicker(m.config.HeartbeatPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkDataNodes()
		}
	}
}

// checkDataNodes 检查数据节点健康状态
func (m *Master) checkDataNodes() {
	m.mu.Lock()
	defer m.mu.Unlock()

	timeout := m.config.HeartbeatPeriod * 3
	now := time.Now()

	for id, node := range m.datanodes {
		if now.Sub(node.LastHeartbeat) > timeout {
			if node.Status == common.NodeOnline {
				log.Printf("[Master] 节点 %s 心跳超时，标记为离线", id)
				node.Status = common.NodeOffline
				// 触发副本恢复
				go m.replicaManager.HandleNodeOffline(id)
			}
		}
	}
}

// RegisterDataNode 注册数据节点
func (m *Master) RegisterDataNode(info *common.DataNodeInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	info.Status = common.NodeOnline
	info.LastHeartbeat = time.Now()
	m.datanodes[info.ID] = info

	log.Printf("[Master] 数据节点注册成功: %s (%s:%d)", info.ID, info.Address, info.Port)
	return nil
}

// Heartbeat 处理心跳请求
func (m *Master) Heartbeat(nodeID common.NodeID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, exists := m.datanodes[nodeID]
	if !exists {
		return fmt.Errorf("unknown node: %s", nodeID)
	}

	node.LastHeartbeat = time.Now()
	if node.Status == common.NodeOffline {
		node.Status = common.NodeOnline
		log.Printf("[Master] 节点 %s 重新上线", nodeID)
	}
	return nil
}

// AllocateChunk 分配数据块副本
func (m *Master) AllocateChunk(fileID common.FileID, size int64) (*common.ChunkInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 选择节点存放副本
	nodes := m.selectNodesForReplica()
	if len(nodes) < m.config.ReplicaCount {
		return nil, fmt.Errorf("insufficient data nodes: need %d, have %d",
			m.config.ReplicaCount, len(nodes))
	}

	// 创建数据块信息
	chunkID := common.ChunkID(fmt.Sprintf("chunk_%d", time.Now().UnixNano()))
	chunk := &common.ChunkInfo{
		ID:         chunkID,
		FileID:     fileID,
		Size:       size,
		Replicas:   make([]common.ReplicaInfo, 0, m.config.ReplicaCount),
		CreateTime: time.Now(),
	}

	// 分配副本，第一个为主副本
	for i, nodeID := range nodes[:m.config.ReplicaCount] {
		role := common.RoleSecondary
		if i == 0 {
			role = common.RolePrimary
		}
		replica := common.ReplicaInfo{
			ChunkID:    chunkID,
			NodeID:     nodeID,
			Role:       role,
			Status:     common.ReplicaHealthy,
			UpdateTime: time.Now(),
		}
		chunk.Replicas = append(chunk.Replicas, replica)
	}

	m.chunks[chunkID] = chunk
	log.Printf("[Master] 分配数据块: %s, 主副本节点: %s", chunkID, nodes[0])

	return chunk, nil
}

// selectNodesForReplica 选择存放副本的节点
func (m *Master) selectNodesForReplica() []common.NodeID {
	var available []common.NodeID
	for id, node := range m.datanodes {
		if node.Status == common.NodeOnline {
			available = append(available, id)
		}
	}
	// TODO: 实现更智能的节点选择策略（考虑负载均衡、机架感知等）
	return available
}

// GetChunkInfo 获取数据块信息
func (m *Master) GetChunkInfo(chunkID common.ChunkID) (*common.ChunkInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	chunk, exists := m.chunks[chunkID]
	if !exists {
		return nil, fmt.Errorf("chunk not found: %s", chunkID)
	}
	return chunk, nil
}

// GetPrimaryNode 获取数据块的主副本节点
func (m *Master) GetPrimaryNode(chunkID common.ChunkID) (common.NodeID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	chunk, exists := m.chunks[chunkID]
	if !exists {
		return "", fmt.Errorf("chunk not found: %s", chunkID)
	}

	for _, replica := range chunk.Replicas {
		if replica.Role == common.RolePrimary {
			return replica.NodeID, nil
		}
	}
	return "", fmt.Errorf("no primary replica found for chunk: %s", chunkID)
}

// GetDataNodeInfo 获取数据节点信息
func (m *Master) GetDataNodeInfo(nodeID common.NodeID) (*common.DataNodeInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, exists := m.datanodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("node not found: %s", nodeID)
	}
	return node, nil
}

// handleRegister 处理节点注册请求
func (m *Master) handleRegister(conn net.Conn, msg *protocol.Message) {
	var req protocol.RegisterRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[Master] 解析注册请求失败: %v", err)
		return
	}

	log.Printf("[Master] 收到节点注册: %s (%s:%d)", req.NodeID, req.Address, req.Port)

	// 注册节点
	nodeInfo := &common.DataNodeInfo{
		ID:            common.NodeID(req.NodeID),
		Address:       req.Address,
		Port:          req.Port,
		TotalSpace:    req.TotalSpace,
		UsedSpace:     req.UsedSpace,
		Status:        common.NodeOnline,
		LastHeartbeat: time.Now(),
	}

	m.mu.Lock()
	m.datanodes[nodeInfo.ID] = nodeInfo
	m.mu.Unlock()

	log.Printf("[Master] 节点注册成功: %s, 当前节点数: %d", req.NodeID, len(m.datanodes))

	// 发送响应
	resp := protocol.RegisterResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
	}
	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleHeartbeat 处理心跳请求
func (m *Master) handleHeartbeat(conn net.Conn, msg *protocol.Message) {
	var req protocol.HeartbeatRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[Master] 解析心跳请求失败: %v", err)
		return
	}

	m.mu.Lock()
	if node, exists := m.datanodes[common.NodeID(req.NodeID)]; exists {
		node.LastHeartbeat = time.Now()
		node.ChunkCount = req.ChunkCount
		node.UsedSpace = req.UsedSpace
		if node.Status == common.NodeOffline {
			node.Status = common.NodeOnline
			log.Printf("[Master] 节点重新上线: %s", req.NodeID)
		}
	}
	m.mu.Unlock()

	// 发送响应
	resp := protocol.HeartbeatResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   true,
		},
	}
	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleAllocChunk 处理数据块分配请求
func (m *Master) handleAllocChunk(conn net.Conn, msg *protocol.Message) {
	var req protocol.AllocChunkRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[Master] 解析分配请求失败: %v", err)
		return
	}

	log.Printf("[Master] 收到数据块分配请求: FileID=%s, Size=%d", req.FileID, req.FileSize)

	// 计算需要的数据块数量
	chunkSize := int64(64 * 1024 * 1024) // 64MB
	chunkCount := (req.FileSize + chunkSize - 1) / chunkSize
	if chunkCount == 0 {
		chunkCount = 1
	}

	resp := protocol.AllocChunkResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
		},
		Chunks: make([]protocol.ChunkLocation, 0),
	}

	// 获取可用节点
	m.mu.Lock()
	var availableNodes []common.NodeID
	for id, node := range m.datanodes {
		if node.Status == common.NodeOnline {
			availableNodes = append(availableNodes, id)
		}
	}

	if len(availableNodes) < m.config.ReplicaCount {
		m.mu.Unlock()
		resp.Success = false
		resp.Error = fmt.Sprintf("可用节点不足: 需要%d, 当前%d", m.config.ReplicaCount, len(availableNodes))
		log.Printf("[Master] %s", resp.Error)
	} else {
		// 为每个数据块分配副本
		for i := int64(0); i < chunkCount; i++ {
			chunkID := common.ChunkID(fmt.Sprintf("chunk_%s_%d", req.FileID, i))
			size := chunkSize
			if i == chunkCount-1 {
				size = req.FileSize - i*chunkSize
			}

			// 选择节点并分配角色
			nodes := make([]string, 0)
			replicaInfos := make([]protocol.ReplicaNodeInfo, 0)
			var primaryAddr string

			for j := 0; j < m.config.ReplicaCount && j < len(availableNodes); j++ {
				idx := (int(i) + j) % len(availableNodes)
				node := m.datanodes[availableNodes[idx]]
				nodeAddr := fmt.Sprintf("%s:%d", node.Address, node.Port)
				nodes = append(nodes, nodeAddr)

				role := "SECONDARY"
				if j == 0 {
					role = "PRIMARY"
					primaryAddr = nodeAddr
				}

				replicaInfos = append(replicaInfos, protocol.ReplicaNodeInfo{
					NodeID:  string(availableNodes[idx]),
					Address: nodeAddr,
					Role:    role,
				})
			}

			loc := protocol.ChunkLocation{
				ChunkID: string(chunkID),
				Index:   int(i),
				Size:    size,
				Nodes:   nodes,
				Primary: primaryAddr,
			}
			resp.Chunks = append(resp.Chunks, loc)

			// 记录到元数据
			chunk := &common.ChunkInfo{
				ID:         chunkID,
				FileID:     common.FileID(req.FileID),
				Index:      int(i),
				Size:       size,
				CreateTime: time.Now(),
				Replicas:   make([]common.ReplicaInfo, 0),
			}

			// 记录副本信息
			for j, repInfo := range replicaInfos {
				role := common.RoleSecondary
				if j == 0 {
					role = common.RolePrimary
				}
				chunk.Replicas = append(chunk.Replicas, common.ReplicaInfo{
					ChunkID:    chunkID,
					NodeID:     common.NodeID(repInfo.NodeID),
					Role:       role,
					Status:     common.ReplicaHealthy,
					UpdateTime: time.Now(),
				})
			}

			m.chunks[chunkID] = chunk

			// 更新文件的ChunkIDs
			if file, ok := m.files[common.FileID(req.FileID)]; ok {
				file.ChunkIDs = append(file.ChunkIDs, chunkID)
			}

			// 异步通知数据节点设置角色
			go m.notifyNodesSetRole(string(chunkID), replicaInfos)
		}
		resp.Success = true
		log.Printf("[Master] 分配了%d个数据块", chunkCount)
	}
	m.mu.Unlock()

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// notifyNodesSetRole 通知数据节点设置角色
func (m *Master) notifyNodesSetRole(chunkID string, replicas []protocol.ReplicaNodeInfo) {
	for _, repInfo := range replicas {
		go func(nodeAddr, role string) {
			if err := m.sendSetRoleToNode(chunkID, nodeAddr, role, replicas); err != nil {
				log.Printf("[Master] 通知节点 %s 设置角色失败: %v", nodeAddr, err)
			} else {
				log.Printf("[Master] 通知节点 %s 设置角色成功: %s", nodeAddr, role)
			}
		}(repInfo.Address, repInfo.Role)
	}
}

// sendSetRoleToNode 发送角色设置请求到数据节点
func (m *Master) sendSetRoleToNode(chunkID, nodeAddr, role string, replicas []protocol.ReplicaNodeInfo) error {
	// 处理 0.0.0.0 地址
	if len(nodeAddr) > 7 && nodeAddr[:7] == "0.0.0.0" {
		nodeAddr = "127.0.0.1" + nodeAddr[7:]
	}

	conn, err := net.DialTimeout("tcp", nodeAddr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("连接节点失败: %w", err)
	}
	defer conn.Close()

	// 找到主副本节点地址
	var primaryNode string
	for _, r := range replicas {
		if r.Role == "PRIMARY" {
			primaryNode = r.Address
			break
		}
	}

	req := protocol.SetRoleRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("setrole_%d", time.Now().UnixNano()),
		},
		ChunkID:     chunkID,
		Role:        role,
		PrimaryNode: primaryNode,
		Replicas:    replicas,
	}

	payload, _ := protocol.EncodePayload(req)
	if err := protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeSetRole,
		Payload: payload,
	}); err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}

	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}

	var resp protocol.SetRoleResponse
	if err := protocol.DecodePayload(respMsg.Payload, &resp); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("设置角色失败: %s", resp.Error)
	}

	return nil
}

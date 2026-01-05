// Package master 集群化 Master 实现
// 集成 Raft 协议实现多 Master 高可用
package master

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/lumeofs/lumeofs/internal/raft"
	"github.com/lumeofs/lumeofs/pkg/common"
	"github.com/lumeofs/lumeofs/pkg/config"
	"github.com/lumeofs/lumeofs/pkg/protocol"
)

// ClusterMaster 集群化主节点
type ClusterMaster struct {
	config config.ClusterMasterConfig
	mu     sync.RWMutex

	// Raft 节点
	raftNode *raft.RaftNode

	// 元数据状态机
	stateMachine *raft.MetadataStateMachine

	// 副本管理器
	replicaManager *ReplicaManager

	// 服务状态
	running  bool
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewClusterMaster 创建集群化主节点
func NewClusterMaster(cfg config.ClusterMasterConfig) (*ClusterMaster, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 创建状态机
	sm := raft.NewMetadataStateMachine()

	// 创建 Raft 配置
	raftCfg := raft.Config{
		NodeID:            cfg.NodeID,
		Address:           cfg.Address,
		Port:              cfg.RaftPort,
		ElectionTimeout:   time.Duration(cfg.ElectionTimeout) * time.Millisecond,
		HeartbeatInterval: time.Duration(cfg.HeartbeatInterval) * time.Millisecond,
		DataDir:           cfg.DataDir,
	}

	// 配置对等节点
	for _, peer := range cfg.Peers {
		raftCfg.Peers = append(raftCfg.Peers, raft.PeerConfig{
			NodeID:  peer.NodeID,
			Address: peer.Address,
			Port:    peer.RaftPort,
		})
	}

	// 创建 Raft 节点
	raftNode, err := raft.NewRaftNode(raftCfg, sm)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("创建 Raft 节点失败: %w", err)
	}

	cm := &ClusterMaster{
		config:       cfg,
		raftNode:     raftNode,
		stateMachine: sm,
		ctx:          ctx,
		cancel:       cancel,
	}

	// 创建副本管理器（复用原有逻辑）
	cm.replicaManager = &ReplicaManager{
		replicaCount: cfg.ReplicaCount,
	}

	return cm, nil
}

// Start 启动集群化主节点
func (cm *ClusterMaster) Start() error {
	// 启动 Raft
	if err := cm.raftNode.Start(); err != nil {
		return fmt.Errorf("启动 Raft 失败: %w", err)
	}

	// 启动客户端监听
	addr := fmt.Sprintf("%s:%d", cm.config.Address, cm.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听失败: %w", err)
	}
	cm.listener = listener
	cm.running = true

	log.Printf("[ClusterMaster] 节点 %s 启动成功", cm.config.NodeID)
	log.Printf("[ClusterMaster] 客户端端口: %d, Raft 端口: %d", cm.config.Port, cm.config.RaftPort)

	// 启动心跳检测
	go cm.heartbeatChecker()

	// 启动连接处理
	go cm.acceptConnections()

	return nil
}

// Stop 停止集群化主节点
func (cm *ClusterMaster) Stop() error {
	cm.cancel()
	cm.running = false

	if cm.raftNode != nil {
		cm.raftNode.Stop()
	}

	if cm.listener != nil {
		return cm.listener.Close()
	}
	return nil
}

// acceptConnections 处理客户端连接
func (cm *ClusterMaster) acceptConnections() {
	for cm.running {
		conn, err := cm.listener.Accept()
		if err != nil {
			if cm.running {
				log.Printf("[ClusterMaster] 接受连接失败: %v", err)
			}
			continue
		}
		go cm.handleConnection(conn)
	}
}

// handleConnection 处理单个连接
func (cm *ClusterMaster) handleConnection(conn net.Conn) {
	defer conn.Close()

	for {
		msg, err := protocol.ReadMessage(conn)
		if err != nil {
			return
		}

		// 检查是否是 Leader
		if !cm.raftNode.IsLeader() && cm.isWriteRequest(msg.Type) {
			// 转发到 Leader
			cm.redirectToLeader(conn, msg)
			return
		}

		switch msg.Type {
		case protocol.MsgTypeRegister:
			cm.handleRegister(conn, msg)
		case protocol.MsgTypeHeartbeat:
			cm.handleHeartbeat(conn, msg)
		case protocol.MsgTypePutFile:
			cm.handlePutFile(conn, msg)
		case protocol.MsgTypeGetFile:
			cm.handleGetFile(conn, msg)
		case protocol.MsgTypeGetStatus:
			cm.handleGetStatus(conn, msg)
		case protocol.MsgTypeAllocChunk:
			cm.handleAllocChunk(conn, msg)
		default:
			log.Printf("[ClusterMaster] 未知消息类型: %d", msg.Type)
		}
	}
}

// isWriteRequest 判断是否是写请求
func (cm *ClusterMaster) isWriteRequest(msgType protocol.MsgType) bool {
	switch msgType {
	case protocol.MsgTypeRegister, protocol.MsgTypePutFile,
		protocol.MsgTypeDeleteFile, protocol.MsgTypeAllocChunk:
		return true
	default:
		return false
	}
}

// redirectToLeader 转发请求到 Leader
func (cm *ClusterMaster) redirectToLeader(conn net.Conn, msg *protocol.Message) {
	leaderAddr := cm.raftNode.GetLeaderAddress()
	if leaderAddr == "" {
		// 无 Leader，返回错误
		resp := protocol.Response{
			Success: false,
			Error:   "no leader available, cluster may be electing",
		}
		payload, _ := protocol.EncodePayload(resp)
		protocol.WriteMessage(conn, &protocol.Message{
			Type:    protocol.MsgTypeResponse,
			Payload: payload,
		})
		return
	}

	// 告知客户端 Leader 地址
	resp := struct {
		protocol.Response
		LeaderAddress string `json:"leader_address"`
	}{
		Response: protocol.Response{
			Success: false,
			Error:   "not leader",
		},
		LeaderAddress: leaderAddr,
	}
	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// ===========================================
// 请求处理（通过 Raft 复制）
// ===========================================

// handleRegister 处理节点注册
func (cm *ClusterMaster) handleRegister(conn net.Conn, msg *protocol.Message) {
	var req protocol.RegisterRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[ClusterMaster] 解析注册请求失败: %v", err)
		return
	}

	log.Printf("[ClusterMaster] 收到节点注册: %s (%s:%d)", req.NodeID, req.Address, req.Port)

	// 构造 Raft 命令
	data, _ := json.Marshal(raft.RegisterNodeData{
		NodeID:     req.NodeID,
		Address:    req.Address,
		Port:       req.Port,
		TotalSpace: req.TotalSpace,
		UsedSpace:  req.UsedSpace,
	})

	cmd := &raft.MetadataCommand{
		Type:      raft.CmdRegisterNode,
		Key:       req.NodeID,
		Value:     data,
		Timestamp: time.Now().UnixNano(),
	}

	// 提交到 Raft
	err := cm.raftNode.Propose(cmd)

	resp := protocol.RegisterResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   err == nil,
		},
	}
	if err != nil {
		resp.Error = err.Error()
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleHeartbeat 处理心跳（不需要 Raft 复制）
func (cm *ClusterMaster) handleHeartbeat(conn net.Conn, msg *protocol.Message) {
	var req protocol.HeartbeatRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		return
	}

	// 直接更新状态机（心跳不需要 Raft 复制）
	cm.stateMachine.UpdateNodeHeartbeat(
		common.NodeID(req.NodeID),
		req.ChunkCount,
		req.UsedSpace,
	)

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

// handlePutFile 处理文件上传请求
func (cm *ClusterMaster) handlePutFile(conn net.Conn, msg *protocol.Message) {
	var req protocol.PutFileRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		log.Printf("[ClusterMaster] 解析上传请求失败: %v", err)
		return
	}

	log.Printf("[ClusterMaster] 收到上传请求: %s, 大小: %d bytes", req.FileName, req.FileSize)

	// 生成文件 ID
	fileID := fmt.Sprintf("file_%d", time.Now().UnixNano())

	// 构造 Raft 命令
	data, _ := json.Marshal(raft.CreateFileData{
		FileID:   fileID,
		FileName: req.FileName,
		FilePath: req.FilePath,
		FileSize: req.FileSize,
		Checksum: req.Checksum,
	})

	cmd := &raft.MetadataCommand{
		Type:      raft.CmdCreateFile,
		Key:       fileID,
		Value:     data,
		Timestamp: time.Now().UnixNano(),
	}

	// 提交到 Raft
	err := cm.raftNode.Propose(cmd)

	resp := protocol.PutFileResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
			Success:   err == nil,
		},
		FileID: fileID,
	}
	if err != nil {
		resp.Error = err.Error()
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleGetFile 处理文件下载请求
func (cm *ClusterMaster) handleGetFile(conn net.Conn, msg *protocol.Message) {
	var req protocol.GetFileRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		return
	}

	// 从状态机查询
	file := cm.stateMachine.GetFileByPath(req.FilePath)

	resp := protocol.GetFileResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
		},
	}

	if file != nil {
		resp.Success = true
		resp.FileName = file.Name
		resp.FileSize = file.Size
	} else {
		resp.Success = false
		resp.Error = "file not found"
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleGetStatus 处理状态查询请求
func (cm *ClusterMaster) handleGetStatus(conn net.Conn, msg *protocol.Message) {
	var req protocol.StatusRequest
	protocol.DecodePayload(msg.Payload, &req)

	// 从状态机获取统计
	fileCount, chunkCount, nodeCount := cm.stateMachine.GetStats()
	nodes := cm.stateMachine.ListDataNodes()

	// 获取 Raft 集群信息
	clusterInfo := cm.raftNode.GetClusterInfo()

	nodeStatuses := make([]protocol.NodeStatus, 0)
	for _, node := range nodes {
		status := "offline"
		if node.Status == common.NodeOnline {
			status = "online"
		}
		nodeStatuses = append(nodeStatuses, protocol.NodeStatus{
			NodeID:     string(node.ID),
			Address:    fmt.Sprintf("%s:%d", node.Address, node.Port),
			Status:     status,
			ChunkCount: node.ChunkCount,
		})
	}

	resp := struct {
		protocol.StatusResponse
		RaftState  string `json:"raft_state"`
		RaftTerm   uint64 `json:"raft_term"`
		RaftLeader string `json:"raft_leader"`
	}{
		StatusResponse: protocol.StatusResponse{
			Response: protocol.Response{
				RequestID: req.RequestID,
				Success:   true,
			},
			NodeCount:  nodeCount,
			FileCount:  fileCount,
			ChunkCount: chunkCount,
			Nodes:      nodeStatuses,
		},
		RaftState:  clusterInfo.State,
		RaftTerm:   clusterInfo.Term,
		RaftLeader: clusterInfo.LeaderID,
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// handleAllocChunk 处理数据块分配请求
func (cm *ClusterMaster) handleAllocChunk(conn net.Conn, msg *protocol.Message) {
	var req protocol.AllocChunkRequest
	if err := protocol.DecodePayload(msg.Payload, &req); err != nil {
		return
	}

	log.Printf("[ClusterMaster] 收到数据块分配请求: FileID=%s, Size=%d", req.FileID, req.FileSize)

	// 获取在线节点
	nodes := cm.stateMachine.GetOnlineNodes()

	resp := protocol.AllocChunkResponse{
		Response: protocol.Response{
			RequestID: req.RequestID,
		},
		Chunks: make([]protocol.ChunkLocation, 0),
	}

	if len(nodes) < cm.config.ReplicaCount {
		resp.Success = false
		resp.Error = fmt.Sprintf("可用节点不足: 需要%d, 当前%d", cm.config.ReplicaCount, len(nodes))
	} else {
		chunkSize := int64(64 * 1024 * 1024)
		chunkCount := (req.FileSize + chunkSize - 1) / chunkSize
		if chunkCount == 0 {
			chunkCount = 1
		}

		for i := int64(0); i < chunkCount; i++ {
			chunkID := fmt.Sprintf("chunk_%s_%d", req.FileID, i)
			size := chunkSize
			if i == chunkCount-1 {
				size = req.FileSize - i*chunkSize
			}

			// 选择节点
			selectedNodes := make([]string, 0)
			nodeAddrs := make([]string, 0)
			var primaryAddr string

			for j := 0; j < cm.config.ReplicaCount && j < len(nodes); j++ {
				idx := (int(i) + j) % len(nodes)
				node := nodes[idx]
				selectedNodes = append(selectedNodes, string(node.ID))
				addr := fmt.Sprintf("%s:%d", node.Address, node.Port)
				nodeAddrs = append(nodeAddrs, addr)
				if j == 0 {
					primaryAddr = addr
				}
			}

			// 通过 Raft 复制
			data, _ := json.Marshal(raft.AllocChunkData{
				ChunkID:  chunkID,
				FileID:   req.FileID,
				Index:    int(i),
				Size:     size,
				Replicas: selectedNodes,
			})

			cmd := &raft.MetadataCommand{
				Type:      raft.CmdAllocChunk,
				Key:       chunkID,
				Value:     data,
				Timestamp: time.Now().UnixNano(),
			}
			cm.raftNode.Propose(cmd)

			resp.Chunks = append(resp.Chunks, protocol.ChunkLocation{
				ChunkID: chunkID,
				Index:   int(i),
				Size:    size,
				Nodes:   nodeAddrs,
				Primary: primaryAddr,
			})
		}
		resp.Success = true
		log.Printf("[ClusterMaster] 分配了%d个数据块", chunkCount)
	}

	payload, _ := protocol.EncodePayload(resp)
	protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeResponse,
		Payload: payload,
	})
}

// ===========================================
// 心跳检测
// ===========================================

func (cm *ClusterMaster) heartbeatChecker() {
	ticker := time.NewTicker(time.Duration(cm.config.HeartbeatPeriod) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-cm.ctx.Done():
			return
		case <-ticker.C:
			cm.checkDataNodes()
		}
	}
}

func (cm *ClusterMaster) checkDataNodes() {
	// 只有 Leader 执行检查
	if !cm.raftNode.IsLeader() {
		return
	}

	nodes := cm.stateMachine.ListDataNodes()
	timeout := time.Duration(cm.config.HeartbeatPeriod*3) * time.Millisecond
	now := time.Now()

	for _, node := range nodes {
		if node.Status == common.NodeOnline && now.Sub(node.LastHeartbeat) > timeout {
			log.Printf("[ClusterMaster] 节点 %s 心跳超时", node.ID)
			cm.stateMachine.SetNodeOffline(node.ID)
		}
	}
}

// ===========================================
// 集群状态接口
// ===========================================

// IsLeader 是否是 Leader
func (cm *ClusterMaster) IsLeader() bool {
	return cm.raftNode.IsLeader()
}

// GetClusterInfo 获取集群信息
func (cm *ClusterMaster) GetClusterInfo() raft.ClusterInfo {
	return cm.raftNode.GetClusterInfo()
}

// Package raft 实现 Raft 共识协议
// 用于多 Master 节点的元数据一致性和 Leader 选举
package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

// ===========================================
// Raft 节点状态
// ===========================================

// NodeState 节点状态
type NodeState int

const (
	Follower  NodeState = iota // 跟随者
	Candidate                  // 候选者
	Leader                     // 领导者
)

func (s NodeState) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// ===========================================
// Raft 日志条目
// ===========================================

// LogEntry Raft 日志条目
type LogEntry struct {
	Index   uint64           `json:"index"`   // 日志索引
	Term    uint64           `json:"term"`    // 任期号
	Command *MetadataCommand `json:"command"` // 元数据命令
}

// MetadataCommand 元数据操作命令
type MetadataCommand struct {
	Type      string          `json:"type"`      // 命令类型
	Key       string          `json:"key"`       // 键
	Value     json.RawMessage `json:"value"`     // 值（JSON序列化）
	Timestamp int64           `json:"timestamp"` // 时间戳
}

// 命令类型常量
const (
	CmdRegisterNode   = "REGISTER_NODE"   // 注册数据节点
	CmdUnregisterNode = "UNREGISTER_NODE" // 注销数据节点
	CmdCreateFile     = "CREATE_FILE"     // 创建文件
	CmdDeleteFile     = "DELETE_FILE"     // 删除文件
	CmdAllocChunk     = "ALLOC_CHUNK"     // 分配数据块
	CmdUpdateChunk    = "UPDATE_CHUNK"    // 更新数据块
)

// ===========================================
// Raft 节点配置
// ===========================================

// Config Raft 配置
type Config struct {
	NodeID            string        // 本节点 ID
	Address           string        // 本节点地址
	Port              int           // 本节点端口
	Peers             []PeerConfig  // 其他节点配置
	ElectionTimeout   time.Duration // 选举超时（随机范围下限）
	HeartbeatInterval time.Duration // 心跳间隔
	DataDir           string        // 数据目录（持久化）
}

// PeerConfig 对等节点配置
type PeerConfig struct {
	NodeID  string `json:"node_id"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		ElectionTimeout:   150 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}
}

// ===========================================
// Raft 节点核心
// ===========================================

// RaftNode Raft 节点
type RaftNode struct {
	mu sync.RWMutex

	// 配置
	config Config

	// 持久化状态（需要落盘）
	currentTerm uint64     // 当前任期
	votedFor    string     // 本任期投票给了谁
	log         []LogEntry // 日志条目

	// 易失状态
	state       NodeState // 当前状态
	commitIndex uint64    // 已提交的最高日志索引
	lastApplied uint64    // 已应用到状态机的最高日志索引

	// Leader 专用易失状态
	nextIndex  map[string]uint64 // 每个节点下一个要发送的日志索引
	matchIndex map[string]uint64 // 每个节点已复制的最高日志索引

	// 运行时
	leaderID      string           // 当前 Leader ID
	lastHeartbeat time.Time        // 最后收到心跳时间
	peers         map[string]*Peer // 对等节点连接

	// 通道
	applyC         chan LogEntry // 应用到状态机的通道
	electionTimer  *time.Timer   // 选举定时器
	heartbeatTimer *time.Timer   // 心跳定时器

	// 状态机
	stateMachine StateMachine

	// 控制
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 网络
	listener net.Listener
}

// Peer 对等节点连接
type Peer struct {
	ID      string
	Address string
	conn    net.Conn
	mu      sync.Mutex
}

// StateMachine 状态机接口
type StateMachine interface {
	Apply(cmd *MetadataCommand) error
	Snapshot() ([]byte, error)
	Restore(data []byte) error
}

// ===========================================
// Raft RPC 消息
// ===========================================

// RequestVoteRequest 请求投票 RPC
type RequestVoteRequest struct {
	Term         uint64 `json:"term"`           // 候选者任期
	CandidateID  string `json:"candidate_id"`   // 候选者 ID
	LastLogIndex uint64 `json:"last_log_index"` // 候选者最后日志索引
	LastLogTerm  uint64 `json:"last_log_term"`  // 候选者最后日志任期
}

// RequestVoteResponse 请求投票响应
type RequestVoteResponse struct {
	Term        uint64 `json:"term"`         // 当前任期
	VoteGranted bool   `json:"vote_granted"` // 是否投票
}

// AppendEntriesRequest 追加日志 RPC（也用作心跳）
type AppendEntriesRequest struct {
	Term         uint64     `json:"term"`           // Leader 任期
	LeaderID     string     `json:"leader_id"`      // Leader ID
	PrevLogIndex uint64     `json:"prev_log_index"` // 前一条日志索引
	PrevLogTerm  uint64     `json:"prev_log_term"`  // 前一条日志任期
	Entries      []LogEntry `json:"entries"`        // 日志条目（心跳时为空）
	LeaderCommit uint64     `json:"leader_commit"`  // Leader 已提交索引
}

// AppendEntriesResponse 追加日志响应
type AppendEntriesResponse struct {
	Term    uint64 `json:"term"`    // 当前任期
	Success bool   `json:"success"` // 是否成功
	// 优化：快速回退
	ConflictIndex uint64 `json:"conflict_index,omitempty"`
	ConflictTerm  uint64 `json:"conflict_term,omitempty"`
}

// RaftMessage Raft 消息封装
type RaftMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

const (
	MsgRequestVote      = "REQUEST_VOTE"
	MsgRequestVoteRes   = "REQUEST_VOTE_RES"
	MsgAppendEntries    = "APPEND_ENTRIES"
	MsgAppendEntriesRes = "APPEND_ENTRIES_RES"
)

// ===========================================
// Raft 节点创建和启动
// ===========================================

// NewRaftNode 创建 Raft 节点
func NewRaftNode(cfg Config, sm StateMachine) (*RaftNode, error) {
	ctx, cancel := context.WithCancel(context.Background())

	rn := &RaftNode{
		config:       cfg,
		currentTerm:  0,
		votedFor:     "",
		log:          make([]LogEntry, 0),
		state:        Follower,
		commitIndex:  0,
		lastApplied:  0,
		nextIndex:    make(map[string]uint64),
		matchIndex:   make(map[string]uint64),
		peers:        make(map[string]*Peer),
		applyC:       make(chan LogEntry, 1000),
		stateMachine: sm,
		ctx:          ctx,
		cancel:       cancel,
	}

	// 初始化日志（索引从1开始）
	rn.log = append(rn.log, LogEntry{Index: 0, Term: 0})

	// 初始化对等节点
	for _, p := range cfg.Peers {
		rn.peers[p.NodeID] = &Peer{
			ID:      p.NodeID,
			Address: fmt.Sprintf("%s:%d", p.Address, p.Port),
		}
	}

	return rn, nil
}

// Start 启动 Raft 节点
func (rn *RaftNode) Start() error {
	// 初始化随机种子（基于节点ID和时间，确保每个节点有不同的随机序列）
	seed := time.Now().UnixNano()
	for _, c := range rn.config.NodeID {
		seed ^= int64(c) << 8
	}
	rand.Seed(seed)

	// 启动网络监听
	addr := fmt.Sprintf("%s:%d", rn.config.Address, rn.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听失败: %w", err)
	}
	rn.listener = listener

	log.Printf("[Raft] 节点 %s 启动，监听 %s", rn.config.NodeID, addr)

	// 启动选举定时器
	rn.resetElectionTimer()

	// 启动网络接收
	rn.wg.Add(1)
	go rn.acceptLoop()

	// 启动日志应用
	rn.wg.Add(1)
	go rn.applyLoop()

	return nil
}

// Stop 停止 Raft 节点
func (rn *RaftNode) Stop() {
	rn.cancel()
	if rn.listener != nil {
		rn.listener.Close()
	}
	if rn.electionTimer != nil {
		rn.electionTimer.Stop()
	}
	if rn.heartbeatTimer != nil {
		rn.heartbeatTimer.Stop()
	}
	rn.wg.Wait()
	log.Printf("[Raft] 节点 %s 已停止", rn.config.NodeID)
}

// ===========================================
// 核心状态查询
// ===========================================

// IsLeader 是否是 Leader
func (rn *RaftNode) IsLeader() bool {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return rn.state == Leader
}

// GetState 获取当前状态
func (rn *RaftNode) GetState() (NodeState, uint64, string) {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return rn.state, rn.currentTerm, rn.leaderID
}

// GetLeaderAddress 获取 Leader 地址
func (rn *RaftNode) GetLeaderAddress() string {
	rn.mu.RLock()
	defer rn.mu.RUnlock()

	if rn.state == Leader {
		return fmt.Sprintf("%s:%d", rn.config.Address, rn.config.Port)
	}

	if peer, ok := rn.peers[rn.leaderID]; ok {
		return peer.Address
	}
	return ""
}

// ===========================================
// 选举相关
// ===========================================

// resetElectionTimer 重置选举定时器
func (rn *RaftNode) resetElectionTimer() {
	if rn.electionTimer != nil {
		rn.electionTimer.Stop()
	}

	// 随机选举超时：150-300ms
	timeout := rn.config.ElectionTimeout +
		time.Duration(rand.Int63n(int64(rn.config.ElectionTimeout)))

	rn.electionTimer = time.AfterFunc(timeout, func() {
		rn.startElection()
	})
}

// startElection 发起选举
func (rn *RaftNode) startElection() {
	rn.mu.Lock()

	// 转为候选者
	rn.state = Candidate
	rn.currentTerm++
	rn.votedFor = rn.config.NodeID
	currentTerm := rn.currentTerm
	lastLogIndex := rn.getLastLogIndex()
	lastLogTerm := rn.getLastLogTerm()

	log.Printf("[Raft] 节点 %s 发起选举，任期 %d", rn.config.NodeID, currentTerm)
	rn.mu.Unlock()

	// 重置选举定时器
	rn.resetElectionTimer()

	// 统计票数（自己投自己）
	votes := 1
	voteCh := make(chan bool, len(rn.peers))

	// 并行请求投票
	for _, peer := range rn.peers {
		go func(p *Peer) {
			req := RequestVoteRequest{
				Term:         currentTerm,
				CandidateID:  rn.config.NodeID,
				LastLogIndex: lastLogIndex,
				LastLogTerm:  lastLogTerm,
			}
			resp, err := rn.sendRequestVote(p, &req)
			if err != nil {
				log.Printf("[Raft] 请求投票到 %s 失败: %v", p.ID, err)
				voteCh <- false
				return
			}

			rn.mu.Lock()
			if resp.Term > rn.currentTerm {
				rn.stepDown(resp.Term)
				rn.mu.Unlock()
				voteCh <- false
				return
			}
			rn.mu.Unlock()

			voteCh <- resp.VoteGranted
		}(peer)
	}

	// 收集投票结果
	for range rn.peers {
		if <-voteCh {
			votes++
		}
	}

	// 检查是否获得多数票
	rn.mu.Lock()
	defer rn.mu.Unlock()

	if rn.state != Candidate || rn.currentTerm != currentTerm {
		return // 状态已改变
	}

	majority := (len(rn.peers)+1)/2 + 1
	if votes >= majority {
		log.Printf("[Raft] 节点 %s 当选 Leader，获得 %d/%d 票",
			rn.config.NodeID, votes, len(rn.peers)+1)
		rn.becomeLeader()
	} else {
		log.Printf("[Raft] 节点 %s 选举失败，获得 %d/%d 票",
			rn.config.NodeID, votes, len(rn.peers)+1)
	}
}

// becomeLeader 成为 Leader
func (rn *RaftNode) becomeLeader() {
	rn.state = Leader
	rn.leaderID = rn.config.NodeID

	// 初始化 nextIndex 和 matchIndex
	lastLogIndex := rn.getLastLogIndex()
	for id := range rn.peers {
		rn.nextIndex[id] = lastLogIndex + 1
		rn.matchIndex[id] = 0
	}

	// 停止选举定时器
	if rn.electionTimer != nil {
		rn.electionTimer.Stop()
	}

	log.Printf("[Raft] 节点 %s 成为 Leader，任期 %d", rn.config.NodeID, rn.currentTerm)

	// 异步启动心跳定时器（避免死锁，因为调用者可能持有锁）
	go rn.startHeartbeat()
}

// stepDown 降级为 Follower
func (rn *RaftNode) stepDown(term uint64) {
	rn.currentTerm = term
	rn.state = Follower
	rn.votedFor = ""

	if rn.heartbeatTimer != nil {
		rn.heartbeatTimer.Stop()
	}

	rn.resetElectionTimer()
}

// ===========================================
// 心跳和日志复制
// ===========================================

// startHeartbeat 启动心跳
func (rn *RaftNode) startHeartbeat() {
	rn.sendHeartbeats()

	rn.heartbeatTimer = time.AfterFunc(rn.config.HeartbeatInterval, func() {
		rn.mu.RLock()
		isLeader := rn.state == Leader
		rn.mu.RUnlock()

		if isLeader {
			rn.startHeartbeat()
		}
	})
}

// sendHeartbeats 发送心跳
func (rn *RaftNode) sendHeartbeats() {
	rn.mu.RLock()
	currentTerm := rn.currentTerm
	commitIndex := rn.commitIndex
	isLeader := rn.state == Leader
	rn.mu.RUnlock()

	if !isLeader {
		return
	}

	log.Printf("[Raft] Leader %s 发送心跳, term %d", rn.config.NodeID, currentTerm)

	for _, peer := range rn.peers {
		go func(p *Peer) {
			rn.mu.RLock()
			prevLogIndex := rn.nextIndex[p.ID] - 1
			prevLogTerm := rn.getLogTerm(prevLogIndex)

			// 获取需要发送的日志
			var entries []LogEntry
			if rn.nextIndex[p.ID] <= rn.getLastLogIndex() {
				entries = rn.log[rn.nextIndex[p.ID]:]
			}
			rn.mu.RUnlock()

			req := AppendEntriesRequest{
				Term:         currentTerm,
				LeaderID:     rn.config.NodeID,
				PrevLogIndex: prevLogIndex,
				PrevLogTerm:  prevLogTerm,
				Entries:      entries,
				LeaderCommit: commitIndex,
			}

			resp, err := rn.sendAppendEntries(p, &req)
			if err != nil {
				log.Printf("[Raft] 心跳发送到 %s 失败: %v", p.ID, err)
				return
			}

			rn.mu.Lock()
			defer rn.mu.Unlock()

			if resp.Term > rn.currentTerm {
				log.Printf("[Raft] 心跳响应 term %d > 当前 term %d，降级", resp.Term, rn.currentTerm)
				rn.stepDown(resp.Term)
				return
			}

			if resp.Success {
				// 更新 nextIndex 和 matchIndex
				if len(entries) > 0 {
					rn.nextIndex[p.ID] = entries[len(entries)-1].Index + 1
					rn.matchIndex[p.ID] = entries[len(entries)-1].Index
					rn.updateCommitIndex()
				}
			} else {
				// 回退 nextIndex
				if resp.ConflictIndex > 0 {
					rn.nextIndex[p.ID] = resp.ConflictIndex
				} else if rn.nextIndex[p.ID] > 1 {
					rn.nextIndex[p.ID]--
				}
			}
		}(peer)
	}
}

// updateCommitIndex 更新提交索引
func (rn *RaftNode) updateCommitIndex() {
	// 找到多数派已复制的最高索引
	for n := rn.getLastLogIndex(); n > rn.commitIndex; n-- {
		if rn.log[n].Term != rn.currentTerm {
			continue
		}

		count := 1 // 自己
		for _, matchIdx := range rn.matchIndex {
			if matchIdx >= n {
				count++
			}
		}

		majority := (len(rn.peers)+1)/2 + 1
		if count >= majority {
			rn.commitIndex = n
			log.Printf("[Raft] 提交日志到索引 %d", n)

			// 通知应用日志
			for i := rn.lastApplied + 1; i <= rn.commitIndex; i++ {
				select {
				case rn.applyC <- rn.log[i]:
				default:
				}
			}
			break
		}
	}
}

// ===========================================
// 日志辅助函数
// ===========================================

func (rn *RaftNode) getLastLogIndex() uint64 {
	return uint64(len(rn.log) - 1)
}

func (rn *RaftNode) getLastLogTerm() uint64 {
	return rn.log[len(rn.log)-1].Term
}

func (rn *RaftNode) getLogTerm(index uint64) uint64 {
	if index < uint64(len(rn.log)) {
		return rn.log[index].Term
	}
	return 0
}

// ===========================================
// 提交命令接口
// ===========================================

// Propose 提交命令（仅 Leader 可用）
func (rn *RaftNode) Propose(cmd *MetadataCommand) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	if rn.state != Leader {
		return fmt.Errorf("not leader, current leader: %s", rn.leaderID)
	}

	// 创建日志条目
	entry := LogEntry{
		Index:   rn.getLastLogIndex() + 1,
		Term:    rn.currentTerm,
		Command: cmd,
	}

	// 追加到本地日志
	rn.log = append(rn.log, entry)

	log.Printf("[Raft] Leader 接收命令: type=%s, key=%s", cmd.Type, cmd.Key)

	return nil
}

// ===========================================
// 网络处理
// ===========================================

func (rn *RaftNode) acceptLoop() {
	defer rn.wg.Done()

	for {
		conn, err := rn.listener.Accept()
		if err != nil {
			select {
			case <-rn.ctx.Done():
				return
			default:
				continue
			}
		}
		go rn.handleConnection(conn)
	}
}

func (rn *RaftNode) handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var msg RaftMessage
		if err := decoder.Decode(&msg); err != nil {
			return
		}

		switch msg.Type {
		case MsgRequestVote:
			var req RequestVoteRequest
			json.Unmarshal(msg.Payload, &req)
			resp := rn.handleRequestVote(&req)
			encoder.Encode(resp)

		case MsgAppendEntries:
			var req AppendEntriesRequest
			json.Unmarshal(msg.Payload, &req)
			resp := rn.handleAppendEntries(&req)
			encoder.Encode(resp)
		}
	}
}

// handleRequestVote 处理投票请求
func (rn *RaftNode) handleRequestVote(req *RequestVoteRequest) *RequestVoteResponse {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	resp := &RequestVoteResponse{
		Term:        rn.currentTerm,
		VoteGranted: false,
	}

	// 请求者任期更高，降级
	if req.Term > rn.currentTerm {
		rn.stepDown(req.Term)
	}

	// 任期小于当前任期，拒绝
	if req.Term < rn.currentTerm {
		return resp
	}

	// 检查是否已投票
	if rn.votedFor != "" && rn.votedFor != req.CandidateID {
		return resp
	}

	// 检查日志是否足够新
	lastLogIndex := rn.getLastLogIndex()
	lastLogTerm := rn.getLastLogTerm()

	logOK := req.LastLogTerm > lastLogTerm ||
		(req.LastLogTerm == lastLogTerm && req.LastLogIndex >= lastLogIndex)

	if logOK {
		rn.votedFor = req.CandidateID
		rn.resetElectionTimer()
		resp.VoteGranted = true
		log.Printf("[Raft] 投票给 %s，任期 %d", req.CandidateID, req.Term)
	}

	return resp
}

// handleAppendEntries 处理追加日志/心跳
func (rn *RaftNode) handleAppendEntries(req *AppendEntriesRequest) *AppendEntriesResponse {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	resp := &AppendEntriesResponse{
		Term:    rn.currentTerm,
		Success: false,
	}

	// 任期小于当前任期，拒绝
	if req.Term < rn.currentTerm {
		log.Printf("[Raft] 拒绝心跳: Leader term %d < 当前 term %d", req.Term, rn.currentTerm)
		return resp
	}

	// 更新任期
	if req.Term > rn.currentTerm {
		rn.stepDown(req.Term)
	}

	// 重置选举定时器
	rn.resetElectionTimer()
	rn.leaderID = req.LeaderID
	rn.lastHeartbeat = time.Now()
	log.Printf("[Raft] 收到心跳来自 %s, term %d", req.LeaderID, req.Term)

	// 检查日志一致性
	if req.PrevLogIndex > 0 {
		if req.PrevLogIndex >= uint64(len(rn.log)) {
			resp.ConflictIndex = uint64(len(rn.log))
			return resp
		}
		if rn.log[req.PrevLogIndex].Term != req.PrevLogTerm {
			resp.ConflictTerm = rn.log[req.PrevLogIndex].Term
			// 找到冲突任期的第一个索引
			for i := req.PrevLogIndex - 1; i > 0; i-- {
				if rn.log[i].Term != resp.ConflictTerm {
					resp.ConflictIndex = i + 1
					break
				}
			}
			return resp
		}
	}

	// 追加新日志
	for i, entry := range req.Entries {
		index := req.PrevLogIndex + uint64(i) + 1
		if index < uint64(len(rn.log)) {
			if rn.log[index].Term != entry.Term {
				// 冲突，截断
				rn.log = rn.log[:index]
				rn.log = append(rn.log, entry)
			}
		} else {
			rn.log = append(rn.log, entry)
		}
	}

	// 更新提交索引
	if req.LeaderCommit > rn.commitIndex {
		lastNewIndex := req.PrevLogIndex + uint64(len(req.Entries))
		if req.LeaderCommit < lastNewIndex {
			rn.commitIndex = req.LeaderCommit
		} else {
			rn.commitIndex = lastNewIndex
		}

		// 通知应用日志
		for i := rn.lastApplied + 1; i <= rn.commitIndex; i++ {
			select {
			case rn.applyC <- rn.log[i]:
			default:
			}
		}
	}

	resp.Success = true
	return resp
}

// ===========================================
// RPC 发送
// ===========================================

func (rn *RaftNode) sendRequestVote(peer *Peer, req *RequestVoteRequest) (*RequestVoteResponse, error) {
	conn, err := net.DialTimeout("tcp", peer.Address, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload, _ := json.Marshal(req)
	msg := RaftMessage{Type: MsgRequestVote, Payload: payload}

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if err := encoder.Encode(msg); err != nil {
		return nil, err
	}

	var resp RequestVoteResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

func (rn *RaftNode) sendAppendEntries(peer *Peer, req *AppendEntriesRequest) (*AppendEntriesResponse, error) {
	conn, err := net.DialTimeout("tcp", peer.Address, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload, _ := json.Marshal(req)
	msg := RaftMessage{Type: MsgAppendEntries, Payload: payload}

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if err := encoder.Encode(msg); err != nil {
		return nil, err
	}

	var resp AppendEntriesResponse
	if err := decoder.Decode(&resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// ===========================================
// 日志应用循环
// ===========================================

func (rn *RaftNode) applyLoop() {
	defer rn.wg.Done()

	for {
		select {
		case <-rn.ctx.Done():
			return
		case entry := <-rn.applyC:
			if entry.Command != nil {
				if err := rn.stateMachine.Apply(entry.Command); err != nil {
					log.Printf("[Raft] 应用日志失败: %v", err)
				} else {
					log.Printf("[Raft] 应用日志成功: index=%d, type=%s",
						entry.Index, entry.Command.Type)
				}
			}
			rn.mu.Lock()
			rn.lastApplied = entry.Index
			rn.mu.Unlock()
		}
	}
}

// ===========================================
// 集群信息
// ===========================================

// ClusterInfo 集群信息
type ClusterInfo struct {
	NodeID    string       `json:"node_id"`
	State     string       `json:"state"`
	Term      uint64       `json:"term"`
	LeaderID  string       `json:"leader_id"`
	LogLength int          `json:"log_length"`
	CommitIdx uint64       `json:"commit_index"`
	Peers     []PeerStatus `json:"peers"`
}

// PeerStatus 对等节点状态
type PeerStatus struct {
	NodeID     string `json:"node_id"`
	Address    string `json:"address"`
	NextIndex  uint64 `json:"next_index"`
	MatchIndex uint64 `json:"match_index"`
}

// GetClusterInfo 获取集群信息
func (rn *RaftNode) GetClusterInfo() ClusterInfo {
	rn.mu.RLock()
	defer rn.mu.RUnlock()

	info := ClusterInfo{
		NodeID:    rn.config.NodeID,
		State:     rn.state.String(),
		Term:      rn.currentTerm,
		LeaderID:  rn.leaderID,
		LogLength: len(rn.log),
		CommitIdx: rn.commitIndex,
		Peers:     make([]PeerStatus, 0),
	}

	for id, peer := range rn.peers {
		info.Peers = append(info.Peers, PeerStatus{
			NodeID:     id,
			Address:    peer.Address,
			NextIndex:  rn.nextIndex[id],
			MatchIndex: rn.matchIndex[id],
		})
	}

	return info
}

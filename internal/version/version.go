// Package version 实现分布式数据版本控制机制
// 包括：向量时钟、租约机制、时间戳版本检查
package version

import (
	"fmt"
	"sync"
	"time"
)

// ===========================================
// 向量时钟（Vector Clock）实现
// ===========================================

// VectorClock 向量时钟 - 用于追踪分布式系统中的因果关系
type VectorClock struct {
	mu     sync.RWMutex
	clocks map[string]uint64 // nodeID -> logical clock
}

// NewVectorClock 创建新的向量时钟
func NewVectorClock() *VectorClock {
	return &VectorClock{
		clocks: make(map[string]uint64),
	}
}

// Increment 增加指定节点的时钟值
func (vc *VectorClock) Increment(nodeID string) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	vc.clocks[nodeID]++
}

// Get 获取指定节点的时钟值
func (vc *VectorClock) Get(nodeID string) uint64 {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.clocks[nodeID]
}

// Merge 合并两个向量时钟（取每个节点的最大值）
func (vc *VectorClock) Merge(other *VectorClock) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	for nodeID, clock := range other.clocks {
		if clock > vc.clocks[nodeID] {
			vc.clocks[nodeID] = clock
		}
	}
}

// Compare 比较两个向量时钟的关系
// 返回: -1 (vc < other), 0 (并发), 1 (vc > other), 2 (相等)
func (vc *VectorClock) Compare(other *VectorClock) int {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	less := false
	greater := false

	// 收集所有节点
	allNodes := make(map[string]bool)
	for k := range vc.clocks {
		allNodes[k] = true
	}
	for k := range other.clocks {
		allNodes[k] = true
	}

	for nodeID := range allNodes {
		v1 := vc.clocks[nodeID]
		v2 := other.clocks[nodeID]
		if v1 < v2 {
			less = true
		} else if v1 > v2 {
			greater = true
		}
	}

	if less && greater {
		return 0 // 并发（冲突）
	} else if less {
		return -1 // vc happens before other
	} else if greater {
		return 1 // vc happens after other
	}
	return 2 // 相等
}

// Clone 复制向量时钟
func (vc *VectorClock) Clone() *VectorClock {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	newVC := NewVectorClock()
	for k, v := range vc.clocks {
		newVC.clocks[k] = v
	}
	return newVC
}

// ToMap 转换为map（用于序列化）
func (vc *VectorClock) ToMap() map[string]uint64 {
	vc.mu.RLock()
	defer vc.mu.RUnlock()

	result := make(map[string]uint64)
	for k, v := range vc.clocks {
		result[k] = v
	}
	return result
}

// FromMap 从map恢复
func (vc *VectorClock) FromMap(m map[string]uint64) {
	vc.mu.Lock()
	defer vc.mu.Unlock()

	vc.clocks = make(map[string]uint64)
	for k, v := range m {
		vc.clocks[k] = v
	}
}

// ===========================================
// 租约（Lease）机制实现
// ===========================================

// Lease 租约 - 用于实现主副本的临时所有权
type Lease struct {
	mu         sync.RWMutex
	ChunkID    string        // 数据块ID
	HolderNode string        // 持有租约的节点
	GrantedAt  time.Time     // 授予时间
	Duration   time.Duration // 租约有效期
	Version    int64         // 租约版本（每次续约+1）
}

// NewLease 创建新租约
func NewLease(chunkID, holderNode string, duration time.Duration) *Lease {
	return &Lease{
		ChunkID:    chunkID,
		HolderNode: holderNode,
		GrantedAt:  time.Now(),
		Duration:   duration,
		Version:    1,
	}
}

// IsValid 检查租约是否有效
func (l *Lease) IsValid() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return time.Since(l.GrantedAt) < l.Duration
}

// ExpiresAt 获取过期时间
func (l *Lease) ExpiresAt() time.Time {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.GrantedAt.Add(l.Duration)
}

// Renew 续约
func (l *Lease) Renew() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.GrantedAt = time.Now()
	l.Version++
}

// Transfer 转移租约到新节点
func (l *Lease) Transfer(newHolder string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.HolderNode = newHolder
	l.GrantedAt = time.Now()
	l.Version++
}

// RemainingTime 获取剩余有效时间
func (l *Lease) RemainingTime() time.Duration {
	l.mu.RLock()
	defer l.mu.RUnlock()
	remaining := l.Duration - time.Since(l.GrantedAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// LeaseManager 租约管理器
type LeaseManager struct {
	mu       sync.RWMutex
	leases   map[string]*Lease // chunkID -> Lease
	duration time.Duration     // 默认租约时长
}

// NewLeaseManager 创建租约管理器
func NewLeaseManager(defaultDuration time.Duration) *LeaseManager {
	return &LeaseManager{
		leases:   make(map[string]*Lease),
		duration: defaultDuration,
	}
}

// Grant 授予租约
func (lm *LeaseManager) Grant(chunkID, nodeID string) (*Lease, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// 检查是否已有有效租约
	if existing, ok := lm.leases[chunkID]; ok && existing.IsValid() {
		if existing.HolderNode != nodeID {
			return nil, fmt.Errorf("chunk %s already leased to %s", chunkID, existing.HolderNode)
		}
		existing.Renew()
		return existing, nil
	}

	// 创建新租约
	lease := NewLease(chunkID, nodeID, lm.duration)
	lm.leases[chunkID] = lease
	return lease, nil
}

// Revoke 撤销租约
func (lm *LeaseManager) Revoke(chunkID string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	delete(lm.leases, chunkID)
}

// GetLease 获取租约信息
func (lm *LeaseManager) GetLease(chunkID string) *Lease {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.leases[chunkID]
}

// CheckHolder 检查节点是否持有有效租约
func (lm *LeaseManager) CheckHolder(chunkID, nodeID string) bool {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	lease, ok := lm.leases[chunkID]
	if !ok {
		return false
	}
	return lease.HolderNode == nodeID && lease.IsValid()
}

// ===========================================
// 时间戳版本（Timestamp Version）实现
// ===========================================

// TimestampVersion 基于时间戳的版本
type TimestampVersion struct {
	Timestamp int64  // 纳秒级时间戳
	NodeID    string // 创建该版本的节点
	Sequence  uint64 // 同一时间戳内的序列号
}

// NewTimestampVersion 创建新版本
func NewTimestampVersion(nodeID string, sequence uint64) TimestampVersion {
	return TimestampVersion{
		Timestamp: time.Now().UnixNano(),
		NodeID:    nodeID,
		Sequence:  sequence,
	}
}

// Compare 比较两个时间戳版本
// 返回: -1 (v < other), 0 (相等), 1 (v > other)
func (v TimestampVersion) Compare(other TimestampVersion) int {
	if v.Timestamp < other.Timestamp {
		return -1
	} else if v.Timestamp > other.Timestamp {
		return 1
	}
	// 时间戳相同，比较序列号
	if v.Sequence < other.Sequence {
		return -1
	} else if v.Sequence > other.Sequence {
		return 1
	}
	return 0
}

// IsNewerThan 检查是否比另一个版本新
func (v TimestampVersion) IsNewerThan(other TimestampVersion) bool {
	return v.Compare(other) > 0
}

// String 格式化输出
func (v TimestampVersion) String() string {
	return fmt.Sprintf("v%d.%d@%s", v.Timestamp, v.Sequence, v.NodeID)
}

// ===========================================
// 数据版本信息（综合版本控制）
// ===========================================

// DataVersion 数据版本信息 - 综合多种版本控制机制
type DataVersion struct {
	// 逻辑版本号（单调递增）
	LogicalVersion int64 `json:"logical_version"`

	// 向量时钟（用于检测并发冲突）
	VectorClock map[string]uint64 `json:"vector_clock"`

	// 时间戳版本
	Timestamp int64  `json:"timestamp"`
	NodeID    string `json:"node_id"`

	// 校验和（用于数据完整性验证）
	Checksum string `json:"checksum"`

	// 更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// NewDataVersion 创建新的数据版本
func NewDataVersion(nodeID string) *DataVersion {
	return &DataVersion{
		LogicalVersion: 1,
		VectorClock:    map[string]uint64{nodeID: 1},
		Timestamp:      time.Now().UnixNano(),
		NodeID:         nodeID,
		UpdatedAt:      time.Now(),
	}
}

// Increment 增加版本
func (dv *DataVersion) Increment(nodeID string) {
	dv.LogicalVersion++
	dv.VectorClock[nodeID]++
	dv.Timestamp = time.Now().UnixNano()
	dv.NodeID = nodeID
	dv.UpdatedAt = time.Now()
}

// IsNewerThan 检查是否比另一个版本新
func (dv *DataVersion) IsNewerThan(other *DataVersion) bool {
	if other == nil {
		return true
	}
	return dv.LogicalVersion > other.LogicalVersion
}

// IsConsistentWith 检查与另一个版本是否一致
func (dv *DataVersion) IsConsistentWith(other *DataVersion) bool {
	if other == nil {
		return false
	}
	return dv.LogicalVersion == other.LogicalVersion &&
		dv.Checksum == other.Checksum
}

// Clone 复制版本信息
func (dv *DataVersion) Clone() *DataVersion {
	newDV := &DataVersion{
		LogicalVersion: dv.LogicalVersion,
		VectorClock:    make(map[string]uint64),
		Timestamp:      dv.Timestamp,
		NodeID:         dv.NodeID,
		Checksum:       dv.Checksum,
		UpdatedAt:      dv.UpdatedAt,
	}
	for k, v := range dv.VectorClock {
		newDV.VectorClock[k] = v
	}
	return newDV
}

// ===========================================
// 一致性检查器
// ===========================================

// ConsistencyChecker 一致性检查器
type ConsistencyChecker struct {
	// 最大允许的时钟偏差
	MaxClockSkew time.Duration
	// 租约管理器
	LeaseManager *LeaseManager
}

// NewConsistencyChecker 创建一致性检查器
func NewConsistencyChecker(maxSkew time.Duration, leaseDuration time.Duration) *ConsistencyChecker {
	return &ConsistencyChecker{
		MaxClockSkew: maxSkew,
		LeaseManager: NewLeaseManager(leaseDuration),
	}
}

// CheckReadConsistency 检查读取一致性
// 返回: (是否可读, 是否需要从主节点读取, 错误)
func (cc *ConsistencyChecker) CheckReadConsistency(
	chunkID string,
	localVersion *DataVersion,
	readerNodeID string,
	isPrimary bool,
) (canRead bool, needPrimary bool, err error) {
	// 主副本始终可读
	if isPrimary {
		return true, false, nil
	}

	// 检查租约
	lease := cc.LeaseManager.GetLease(chunkID)
	if lease == nil || !lease.IsValid() {
		// 无有效租约，需要从主节点读取以确保一致性
		return false, true, nil
	}

	// 检查版本时间戳是否在允许的偏差范围内
	if localVersion != nil {
		age := time.Since(localVersion.UpdatedAt)
		if age > cc.MaxClockSkew {
			// 本地版本太旧，需要从主节点读取
			return false, true, nil
		}
	}

	return true, false, nil
}

// CheckWritePermission 检查写入权限
func (cc *ConsistencyChecker) CheckWritePermission(
	chunkID string,
	writerNodeID string,
	isPrimary bool,
) (canWrite bool, primaryNode string, err error) {
	// 只有主副本可以直接写入
	if isPrimary {
		return true, "", nil
	}

	// 非主副本需要转发到主节点
	lease := cc.LeaseManager.GetLease(chunkID)
	if lease != nil && lease.IsValid() {
		return false, lease.HolderNode, nil
	}

	return false, "", fmt.Errorf("no valid lease for chunk %s", chunkID)
}

// ValidateVersionConsistency 验证多个副本的版本一致性
func (cc *ConsistencyChecker) ValidateVersionConsistency(versions []*DataVersion) (bool, *DataVersion) {
	if len(versions) == 0 {
		return false, nil
	}

	// 找出最新版本
	var newest *DataVersion
	for _, v := range versions {
		if v != nil && (newest == nil || v.IsNewerThan(newest)) {
			newest = v
		}
	}

	// 检查所有版本是否一致
	consistent := true
	for _, v := range versions {
		if v != nil && !v.IsConsistentWith(newest) {
			consistent = false
			break
		}
	}

	return consistent, newest
}

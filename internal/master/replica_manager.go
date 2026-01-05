// Package master 副本管理器实现
package master

import (
	"log"
	"sync"

	"github.com/lumeofs/lumeofs/pkg/common"
)

// ReplicaManager 副本管理器
type ReplicaManager struct {
	master       *Master
	replicaCount int
	mu           sync.RWMutex
}

// NewReplicaManager 创建副本管理器
func NewReplicaManager(m *Master, replicaCount int) *ReplicaManager {
	return &ReplicaManager{
		master:       m,
		replicaCount: replicaCount,
	}
}

// HandleNodeOffline 处理节点离线事件
func (rm *ReplicaManager) HandleNodeOffline(nodeID common.NodeID) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	log.Printf("[ReplicaManager] 处理节点离线: %s", nodeID)

	// 找到该节点上的所有副本
	affectedChunks := rm.findChunksOnNode(nodeID)

	for _, chunkID := range affectedChunks {
		rm.handleChunkRecovery(chunkID, nodeID)
	}
}

// findChunksOnNode 查找节点上的所有数据块
func (rm *ReplicaManager) findChunksOnNode(nodeID common.NodeID) []common.ChunkID {
	var chunks []common.ChunkID

	rm.master.mu.RLock()
	defer rm.master.mu.RUnlock()

	for chunkID, chunk := range rm.master.chunks {
		for _, replica := range chunk.Replicas {
			if replica.NodeID == nodeID {
				chunks = append(chunks, chunkID)
				break
			}
		}
	}
	return chunks
}

// handleChunkRecovery 处理单个数据块的恢复
func (rm *ReplicaManager) handleChunkRecovery(chunkID common.ChunkID, offlineNode common.NodeID) {
	rm.master.mu.Lock()
	defer rm.master.mu.Unlock()

	chunk, exists := rm.master.chunks[chunkID]
	if !exists {
		return
	}

	// 标记离线节点上的副本状态
	var needPrimaryElection bool
	for i, replica := range chunk.Replicas {
		if replica.NodeID == offlineNode {
			chunk.Replicas[i].Status = common.ReplicaOffline
			if replica.Role == common.RolePrimary {
				needPrimaryElection = true
			}
		}
	}

	// 如果主副本离线，需要选举新的主副本
	if needPrimaryElection {
		rm.electNewPrimary(chunk)
	}

	// 检查是否需要创建新副本
	healthyCount := rm.countHealthyReplicas(chunk)
	if healthyCount < rm.replicaCount {
		log.Printf("[ReplicaManager] 数据块 %s 健康副本不足 (%d/%d)，需要创建新副本",
			chunkID, healthyCount, rm.replicaCount)
		// TODO: 触发副本重建任务
	}
}

// electNewPrimary 选举新的主副本
func (rm *ReplicaManager) electNewPrimary(chunk *common.ChunkInfo) {
	// 从健康的从副本中选择版本号最高的作为新主副本
	var bestCandidate int = -1
	var highestVersion int64 = -1

	for i, replica := range chunk.Replicas {
		if replica.Status == common.ReplicaHealthy && replica.Role == common.RoleSecondary {
			if replica.Version > highestVersion {
				highestVersion = replica.Version
				bestCandidate = i
			}
		}
	}

	if bestCandidate >= 0 {
		// 将旧主副本降级
		for i := range chunk.Replicas {
			if chunk.Replicas[i].Role == common.RolePrimary {
				chunk.Replicas[i].Role = common.RoleSecondary
			}
		}
		// 提升新主副本
		chunk.Replicas[bestCandidate].Role = common.RolePrimary
		log.Printf("[ReplicaManager] 数据块 %s 选举新主副本: 节点 %s",
			chunk.ID, chunk.Replicas[bestCandidate].NodeID)
	} else {
		log.Printf("[ReplicaManager] 警告: 数据块 %s 无法选举新主副本，无健康从副本", chunk.ID)
	}
}

// countHealthyReplicas 统计健康副本数量
func (rm *ReplicaManager) countHealthyReplicas(chunk *common.ChunkInfo) int {
	count := 0
	for _, replica := range chunk.Replicas {
		if replica.Status == common.ReplicaHealthy {
			count++
		}
	}
	return count
}

// VerifyReplicaConsistency 验证副本数据一致性
func (rm *ReplicaManager) VerifyReplicaConsistency(chunkID common.ChunkID) (bool, error) {
	rm.master.mu.RLock()
	chunk, exists := rm.master.chunks[chunkID]
	rm.master.mu.RUnlock()

	if !exists {
		return false, nil
	}

	// 比较所有副本的校验和
	var checksums []string
	for _, replica := range chunk.Replicas {
		if replica.Status == common.ReplicaHealthy {
			checksums = append(checksums, replica.Checksum)
		}
	}

	if len(checksums) == 0 {
		return false, nil
	}

	// 检查所有校验和是否一致
	firstChecksum := checksums[0]
	for _, cs := range checksums[1:] {
		if cs != firstChecksum {
			log.Printf("[ReplicaManager] 数据块 %s 发现数据不一致", chunkID)
			return false, nil
		}
	}

	return true, nil
}

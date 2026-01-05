// Package erasure 实现纠错码（Erasure Code）功能
// 通过纠错码实现数据一致性，纠错码也有三副本
package erasure

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

var (
	ErrInvalidShardCount = errors.New("invalid shard count")
	ErrTooFewShards      = errors.New("too few shards to reconstruct")
	ErrCorruptedData     = errors.New("data is corrupted")
	ErrShardSizeMismatch = errors.New("shard size mismatch")
)

// Config 纠错码配置
type Config struct {
	DataShards   int // 数据分片数
	ParityShards int // 校验分片数
}

// DefaultConfig 默认配置（3个数据分片）
func DefaultConfig() Config {
	return Config{
		DataShards:   3,
		ParityShards: 1,
	}
}

// Encoder 纠错码编码器
type Encoder struct {
	config Config
}

// NewEncoder 创建编码器
func NewEncoder(cfg Config) (*Encoder, error) {
	if cfg.DataShards <= 0 || cfg.ParityShards < 0 {
		return nil, ErrInvalidShardCount
	}
	return &Encoder{config: cfg}, nil
}

// Shard 数据分片
type Shard struct {
	Index    int    `json:"index"`
	Data     []byte `json:"data"`
	Checksum string `json:"checksum"`
	IsParity bool   `json:"is_parity"`
}

// Encode 将数据编码为多个分片
func (e *Encoder) Encode(data []byte) ([]*Shard, error) {
	totalShards := e.config.DataShards + e.config.ParityShards
	shards := make([]*Shard, totalShards)

	// 计算每个分片大小（向上取整）
	shardSize := (len(data) + e.config.DataShards - 1) / e.config.DataShards

	// 填充数据使其能均匀分配
	paddedData := make([]byte, shardSize*e.config.DataShards)
	copy(paddedData, data)

	// 创建数据分片
	for i := 0; i < e.config.DataShards; i++ {
		start := i * shardSize
		end := start + shardSize
		shardData := paddedData[start:end]

		shards[i] = &Shard{
			Index:    i,
			Data:     shardData,
			Checksum: calculateChecksum(shardData),
			IsParity: false,
		}
	}

	// 创建校验分片（使用简单的XOR校验）
	for i := 0; i < e.config.ParityShards; i++ {
		parityData := make([]byte, shardSize)

		// XOR所有数据分片
		for j := 0; j < e.config.DataShards; j++ {
			for k := 0; k < shardSize; k++ {
				parityData[k] ^= shards[j].Data[k]
			}
		}

		shards[e.config.DataShards+i] = &Shard{
			Index:    e.config.DataShards + i,
			Data:     parityData,
			Checksum: calculateChecksum(parityData),
			IsParity: true,
		}
	}

	return shards, nil
}

// Decode 从分片重建原始数据
func (e *Encoder) Decode(shards []*Shard, originalSize int) ([]byte, error) {
	if len(shards) < e.config.DataShards {
		return nil, ErrTooFewShards
	}

	// 验证分片完整性
	validShards := make([]*Shard, 0)
	for _, shard := range shards {
		if shard != nil && verifyChecksum(shard) {
			validShards = append(validShards, shard)
		}
	}

	if len(validShards) < e.config.DataShards {
		return nil, ErrTooFewShards
	}

	// 检查是否需要重建
	dataShards := make([]*Shard, e.config.DataShards)
	missingIndices := make([]int, 0)

	for _, shard := range validShards {
		if !shard.IsParity && shard.Index < e.config.DataShards {
			dataShards[shard.Index] = shard
		}
	}

	for i := 0; i < e.config.DataShards; i++ {
		if dataShards[i] == nil {
			missingIndices = append(missingIndices, i)
		}
	}

	// 如果有缺失的数据分片，尝试使用校验分片重建
	if len(missingIndices) > 0 {
		if err := e.reconstruct(validShards, dataShards, missingIndices); err != nil {
			return nil, err
		}
	}

	// 合并数据分片
	var result bytes.Buffer
	for i := 0; i < e.config.DataShards; i++ {
		if dataShards[i] != nil {
			result.Write(dataShards[i].Data)
		}
	}

	// 截取原始大小
	data := result.Bytes()
	if originalSize > 0 && originalSize < len(data) {
		data = data[:originalSize]
	}

	return data, nil
}

// reconstruct 使用校验分片重建缺失的数据分片
func (e *Encoder) reconstruct(validShards []*Shard, dataShards []*Shard, missingIndices []int) error {
	if len(missingIndices) > e.config.ParityShards {
		return ErrTooFewShards
	}

	// 查找校验分片
	var parityShard *Shard
	for _, shard := range validShards {
		if shard.IsParity {
			parityShard = shard
			break
		}
	}

	if parityShard == nil {
		return ErrTooFewShards
	}

	// 使用XOR重建缺失分片（仅支持单个缺失）
	if len(missingIndices) == 1 {
		missingIdx := missingIndices[0]
		shardSize := len(parityShard.Data)
		reconstructed := make([]byte, shardSize)
		copy(reconstructed, parityShard.Data)

		// XOR所有存在的数据分片
		for i := 0; i < e.config.DataShards; i++ {
			if i != missingIdx && dataShards[i] != nil {
				for k := 0; k < shardSize; k++ {
					reconstructed[k] ^= dataShards[i].Data[k]
				}
			}
		}

		dataShards[missingIdx] = &Shard{
			Index:    missingIdx,
			Data:     reconstructed,
			Checksum: calculateChecksum(reconstructed),
			IsParity: false,
		}

		return nil
	}

	return ErrTooFewShards
}

// Verify 验证所有分片数据一致性
func (e *Encoder) Verify(shards []*Shard) (bool, error) {
	if len(shards) == 0 {
		return false, ErrTooFewShards
	}

	// 验证每个分片的校验和
	for _, shard := range shards {
		if shard != nil && !verifyChecksum(shard) {
			return false, nil
		}
	}

	// 验证校验分片的一致性
	var shardSize int
	for _, shard := range shards {
		if shard != nil {
			shardSize = len(shard.Data)
			break
		}
	}

	if shardSize == 0 {
		return false, ErrShardSizeMismatch
	}

	// 重新计算校验分片并比较
	expectedParity := make([]byte, shardSize)
	for _, shard := range shards {
		if shard != nil && !shard.IsParity {
			for k := 0; k < shardSize; k++ {
				expectedParity[k] ^= shard.Data[k]
			}
		}
	}

	for _, shard := range shards {
		if shard != nil && shard.IsParity {
			if !bytes.Equal(shard.Data, expectedParity) {
				return false, nil
			}
		}
	}

	return true, nil
}

// ReplicatedShard 三副本分片结构
type ReplicatedShard struct {
	Shard    *Shard   `json:"shard"`
	Replicas [][]byte `json:"replicas"` // 3副本数据
}

// CreateReplicatedShards 创建带三副本的分片
func (e *Encoder) CreateReplicatedShards(data []byte, replicaCount int) ([]*ReplicatedShard, error) {
	// 先进行纠错编码
	shards, err := e.Encode(data)
	if err != nil {
		return nil, err
	}

	// 为每个分片创建副本
	replicatedShards := make([]*ReplicatedShard, len(shards))
	for i, shard := range shards {
		replicas := make([][]byte, replicaCount)
		for j := 0; j < replicaCount; j++ {
			replica := make([]byte, len(shard.Data))
			copy(replica, shard.Data)
			replicas[j] = replica
		}
		replicatedShards[i] = &ReplicatedShard{
			Shard:    shard,
			Replicas: replicas,
		}
	}

	return replicatedShards, nil
}

// VerifyReplicas 验证副本一致性
func VerifyReplicas(rs *ReplicatedShard) bool {
	if len(rs.Replicas) == 0 {
		return false
	}

	expectedChecksum := rs.Shard.Checksum
	for _, replica := range rs.Replicas {
		if calculateChecksum(replica) != expectedChecksum {
			return false
		}
	}
	return true
}

// RepairReplica 修复损坏的副本
func RepairReplica(rs *ReplicatedShard, damagedIndex int) error {
	if damagedIndex < 0 || damagedIndex >= len(rs.Replicas) {
		return fmt.Errorf("invalid replica index: %d", damagedIndex)
	}

	// 找到一个健康的副本
	var healthyData []byte
	for i, replica := range rs.Replicas {
		if i != damagedIndex && calculateChecksum(replica) == rs.Shard.Checksum {
			healthyData = replica
			break
		}
	}

	if healthyData == nil {
		return errors.New("no healthy replica found")
	}

	// 修复损坏的副本
	rs.Replicas[damagedIndex] = make([]byte, len(healthyData))
	copy(rs.Replicas[damagedIndex], healthyData)

	return nil
}

// calculateChecksum 计算校验和
func calculateChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// verifyChecksum 验证校验和
func verifyChecksum(shard *Shard) bool {
	return calculateChecksum(shard.Data) == shard.Checksum
}

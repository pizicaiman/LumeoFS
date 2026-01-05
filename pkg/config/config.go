// Package config 提供LumeoFS的配置管理
package config

import (
	"encoding/json"
	"os"
	"time"
)

// MasterConfig 主节点配置
type MasterConfig struct {
	Address         string        `json:"address"`
	Port            int           `json:"port"`
	DataDir         string        `json:"data_dir"`
	ReplicaCount    int           `json:"replica_count"`    // 副本数量，默认3
	HeartbeatPeriod time.Duration `json:"heartbeat_period"` // 心跳检测周期
	MetaDBPath      string        `json:"meta_db_path"`     // 元数据存储路径
}

// DataNodeConfig 数据节点配置
type DataNodeConfig struct {
	NodeID          string        `json:"node_id"`
	Address         string        `json:"address"`
	Port            int           `json:"port"`
	MasterAddress   string        `json:"master_address"`
	MasterPort      int           `json:"master_port"`
	DataDir         string        `json:"data_dir"`
	WALDir          string        `json:"wal_dir"` // WAL日志目录
	HeartbeatPeriod time.Duration `json:"heartbeat_period"`
	MaxChunkSize    int64         `json:"max_chunk_size"`
}

// ErasureConfig 纠错码配置
type ErasureConfig struct {
	DataShards   int  `json:"data_shards"`   // 数据分片数
	ParityShards int  `json:"parity_shards"` // 校验分片数
	Enabled      bool `json:"enabled"`
}

// ClusterConfig 集群总配置
type ClusterConfig struct {
	Master  MasterConfig  `json:"master"`
	Erasure ErasureConfig `json:"erasure"`
}

// ClusterMasterConfig 集群化主节点配置（多 Master 高可用）
type ClusterMasterConfig struct {
	// 基本配置
	NodeID       string `json:"node_id"`       // 本节点 ID
	Address      string `json:"address"`       // 监听地址
	Port         int    `json:"port"`          // 客户端端口
	RaftPort     int    `json:"raft_port"`     // Raft 通信端口
	DataDir      string `json:"data_dir"`      // 数据目录
	ReplicaCount int    `json:"replica_count"` // 副本数量

	// Raft 配置
	ElectionTimeout   int `json:"election_timeout"`   // 选举超时(ms)
	HeartbeatInterval int `json:"heartbeat_interval"` // 心跳间隔(ms)
	HeartbeatPeriod   int `json:"heartbeat_period"`   // 节点心跳检测周期(ms)

	// 集群节点
	Peers []ClusterPeerConfig `json:"peers"` // 其他节点配置
}

// ClusterPeerConfig 集群对等节点配置
type ClusterPeerConfig struct {
	NodeID   string `json:"node_id"`
	Address  string `json:"address"`
	Port     int    `json:"port"`      // 客户端端口
	RaftPort int    `json:"raft_port"` // Raft 端口
}

// DefaultClusterMasterConfig 返回默认集群主节点配置
func DefaultClusterMasterConfig() ClusterMasterConfig {
	return ClusterMasterConfig{
		NodeID:            "master-1",
		Address:           "0.0.0.0",
		Port:              9000,
		RaftPort:          9100,
		DataDir:           "./data/master",
		ReplicaCount:      3,
		ElectionTimeout:   150,  // 150ms
		HeartbeatInterval: 50,   // 50ms
		HeartbeatPeriod:   3000, // 3s
		Peers:             []ClusterPeerConfig{},
	}
}

// DefaultMasterConfig 返回默认主节点配置
func DefaultMasterConfig() MasterConfig {
	return MasterConfig{
		Address:         "0.0.0.0",
		Port:            9000,
		DataDir:         "./data/master",
		ReplicaCount:    3,
		HeartbeatPeriod: 3 * time.Second,
		MetaDBPath:      "./data/master/meta.db",
	}
}

// DefaultDataNodeConfig 返回默认数据节点配置
func DefaultDataNodeConfig() DataNodeConfig {
	return DataNodeConfig{
		NodeID:          "",
		Address:         "0.0.0.0",
		Port:            9001,
		MasterAddress:   "127.0.0.1",
		MasterPort:      9000,
		DataDir:         "./data/node",
		WALDir:          "./data/node/wal",
		HeartbeatPeriod: 3 * time.Second,
		MaxChunkSize:    64 * 1024 * 1024, // 64MB
	}
}

// DefaultErasureConfig 返回默认纠错码配置
func DefaultErasureConfig() ErasureConfig {
	return ErasureConfig{
		DataShards:   3, // 3个数据分片
		ParityShards: 1, // 1个校验分片
		Enabled:      true,
	}
}

// LoadMasterConfig 从文件加载主节点配置
func LoadMasterConfig(path string) (*MasterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg MasterConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadDataNodeConfig 从文件加载数据节点配置
func LoadDataNodeConfig(path string) (*DataNodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg DataNodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SaveConfig 保存配置到文件
func SaveConfig(path string, cfg interface{}) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

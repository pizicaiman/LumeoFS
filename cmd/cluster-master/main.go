// LumeoFS Cluster Master Node 集群主节点启动入口
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/lumeofs/lumeofs/internal/master"
	"github.com/lumeofs/lumeofs/pkg/config"
)

func main() {
	// 命令行参数
	configFile := flag.String("config", "", "配置文件路径")
	nodeID := flag.String("node-id", "master-1", "节点 ID")
	address := flag.String("address", "0.0.0.0", "监听地址")
	port := flag.Int("port", 9000, "客户端端口")
	raftPort := flag.Int("raft-port", 9100, "Raft 端口")
	dataDir := flag.String("data-dir", "./data/master", "数据目录")
	replicas := flag.Int("replicas", 3, "副本数量")
	peers := flag.String("peers", "", "对等节点 (JSON 格式)")
	flag.Parse()

	// 加载配置
	var cfg config.ClusterMasterConfig
	if *configFile != "" {
		data, err := os.ReadFile(*configFile)
		if err != nil {
			log.Fatalf("读取配置文件失败: %v", err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Fatalf("解析配置文件失败: %v", err)
		}
	} else {
		cfg = config.DefaultClusterMasterConfig()
		cfg.NodeID = *nodeID
		cfg.Address = *address
		cfg.Port = *port
		cfg.RaftPort = *raftPort
		cfg.DataDir = *dataDir
		cfg.ReplicaCount = *replicas

		// 解析对等节点
		if *peers != "" {
			var peerList []config.ClusterPeerConfig
			if err := json.Unmarshal([]byte(*peers), &peerList); err != nil {
				log.Fatalf("解析对等节点失败: %v", err)
			}
			cfg.Peers = peerList
		}
	}

	// 创建数据目录
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}

	// 创建集群主节点
	m, err := master.NewClusterMaster(cfg)
	if err != nil {
		log.Fatalf("创建集群主节点失败: %v", err)
	}

	// 启动
	if err := m.Start(); err != nil {
		log.Fatalf("启动集群主节点失败: %v", err)
	}

	log.Println("========================================")
	log.Println("  LumeoFS Cluster Master Node")
	log.Println("  分布式存储系统 - 集群主节点")
	log.Println("========================================")
	log.Printf("  节点 ID: %s", cfg.NodeID)
	log.Printf("  客户端端口: %d", cfg.Port)
	log.Printf("  Raft 端口: %d", cfg.RaftPort)
	log.Printf("  副本数量: %d", cfg.ReplicaCount)
	log.Printf("  对等节点: %d 个", len(cfg.Peers))
	log.Println("========================================")

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("正在关闭集群主节点...")
	if err := m.Stop(); err != nil {
		log.Printf("关闭时出错: %v", err)
	}
	log.Println("集群主节点已关闭")
}

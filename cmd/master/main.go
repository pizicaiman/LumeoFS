// LumeoFS Master Node 主节点启动入口
package main

import (
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
	address := flag.String("address", "0.0.0.0", "监听地址")
	port := flag.Int("port", 9000, "监听端口")
	dataDir := flag.String("data-dir", "./data/master", "数据目录")
	replicaCount := flag.Int("replicas", 3, "副本数量")
	flag.Parse()

	// 加载配置
	var cfg config.MasterConfig
	if *configFile != "" {
		loadedCfg, err := config.LoadMasterConfig(*configFile)
		if err != nil {
			log.Fatalf("加载配置文件失败: %v", err)
		}
		cfg = *loadedCfg
	} else {
		cfg = config.DefaultMasterConfig()
		cfg.Address = *address
		cfg.Port = *port
		cfg.DataDir = *dataDir
		cfg.ReplicaCount = *replicaCount
	}

	// 创建数据目录
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}

	// 启动主节点
	m := master.NewMaster(cfg)
	if err := m.Start(); err != nil {
		log.Fatalf("启动主节点失败: %v", err)
	}

	log.Println("========================================")
	log.Println("  LumeoFS Master Node")
	log.Println("  分布式存储系统 - 主节点")
	log.Println("========================================")
	log.Printf("  监听地址: %s:%d", cfg.Address, cfg.Port)
	log.Printf("  副本数量: %d", cfg.ReplicaCount)
	log.Printf("  数据目录: %s", cfg.DataDir)
	log.Println("========================================")

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("正在关闭主节点...")
	if err := m.Stop(); err != nil {
		log.Printf("关闭主节点时出错: %v", err)
	}
	log.Println("主节点已关闭")
}

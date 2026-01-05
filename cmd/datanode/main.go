// LumeoFS Data Node 数据节点启动入口
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/lumeofs/lumeofs/internal/datanode"
	"github.com/lumeofs/lumeofs/pkg/config"
)

func main() {
	// 命令行参数
	configFile := flag.String("config", "", "配置文件路径")
	nodeID := flag.String("node-id", "", "节点ID")
	address := flag.String("address", "0.0.0.0", "监听地址")
	port := flag.Int("port", 9001, "监听端口")
	masterAddr := flag.String("master", "127.0.0.1", "主节点地址")
	masterPort := flag.Int("master-port", 9000, "主节点端口")
	dataDir := flag.String("data-dir", "./data/node", "数据目录")
	flag.Parse()

	// 加载配置
	var cfg config.DataNodeConfig
	if *configFile != "" {
		loadedCfg, err := config.LoadDataNodeConfig(*configFile)
		if err != nil {
			log.Fatalf("加载配置文件失败: %v", err)
		}
		cfg = *loadedCfg
	} else {
		cfg = config.DefaultDataNodeConfig()
		cfg.NodeID = *nodeID
		cfg.Address = *address
		cfg.Port = *port
		cfg.MasterAddress = *masterAddr
		cfg.MasterPort = *masterPort
		cfg.DataDir = *dataDir
		cfg.WALDir = *dataDir + "/wal"
	}

	// 创建目录
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}
	if err := os.MkdirAll(cfg.WALDir, 0755); err != nil {
		log.Fatalf("创建WAL目录失败: %v", err)
	}

	// 启动数据节点
	dn, err := datanode.NewDataNode(cfg)
	if err != nil {
		log.Fatalf("创建数据节点失败: %v", err)
	}

	if err := dn.Start(); err != nil {
		log.Fatalf("启动数据节点失败: %v", err)
	}

	log.Println("========================================")
	log.Println("  LumeoFS Data Node")
	log.Println("  分布式存储系统 - 数据节点")
	log.Println("========================================")
	log.Printf("  监听地址: %s:%d", cfg.Address, cfg.Port)
	log.Printf("  主节点: %s:%d", cfg.MasterAddress, cfg.MasterPort)
	log.Printf("  数据目录: %s", cfg.DataDir)
	log.Printf("  WAL目录: %s", cfg.WALDir)
	log.Println("========================================")

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("正在关闭数据节点...")
	if err := dn.Stop(); err != nil {
		log.Printf("关闭数据节点时出错: %v", err)
	}
	log.Println("数据节点已关闭")
}

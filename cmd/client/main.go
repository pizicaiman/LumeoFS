// LumeoFS Client CLI 客户端命令行工具
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/lumeofs/lumeofs/pkg/protocol"
)

const version = "0.1.0"

func main() {
	// 子命令
	putCmd := flag.NewFlagSet("put", flag.ExitOnError)
	getCmd := flag.NewFlagSet("get", flag.ExitOnError)
	listCmd := flag.NewFlagSet("list", flag.ExitOnError)
	deleteCmd := flag.NewFlagSet("delete", flag.ExitOnError)
	statusCmd := flag.NewFlagSet("status", flag.ExitOnError)

	// 全局参数
	masterAddr := "127.0.0.1:9000"

	// put命令参数
	putFile := putCmd.String("file", "", "本地文件路径")
	putDest := putCmd.String("dest", "", "目标路径")
	putMaster := putCmd.String("master", masterAddr, "主节点地址")

	// get命令参数
	getPath := getCmd.String("path", "", "远程文件路径")
	getOutput := getCmd.String("output", "", "本地输出路径")
	getMaster := getCmd.String("master", masterAddr, "主节点地址")

	// list命令参数
	listPath := listCmd.String("path", "/", "目录路径")
	listMaster := listCmd.String("master", masterAddr, "主节点地址")

	// delete命令参数
	deletePath := deleteCmd.String("path", "", "文件路径")
	deleteMaster := deleteCmd.String("master", masterAddr, "主节点地址")

	// status命令参数
	statusMaster := statusCmd.String("master", masterAddr, "主节点地址")

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "put":
		putCmd.Parse(os.Args[2:])
		if *putFile == "" || *putDest == "" {
			putCmd.PrintDefaults()
			os.Exit(1)
		}
		handlePut(*putMaster, *putFile, *putDest)

	case "get":
		getCmd.Parse(os.Args[2:])
		if *getPath == "" {
			getCmd.PrintDefaults()
			os.Exit(1)
		}
		handleGet(*getMaster, *getPath, *getOutput)

	case "list", "ls":
		listCmd.Parse(os.Args[2:])
		handleList(*listMaster, *listPath)

	case "delete", "rm":
		deleteCmd.Parse(os.Args[2:])
		if *deletePath == "" {
			deleteCmd.PrintDefaults()
			os.Exit(1)
		}
		handleDelete(*deleteMaster, *deletePath)

	case "status":
		statusCmd.Parse(os.Args[2:])
		handleStatus(*statusMaster)

	case "version":
		fmt.Printf("LumeoFS Client v%s\n", version)

	case "help":
		printUsage()

	default:
		fmt.Printf("未知命令: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("========================================")
	fmt.Println("  LumeoFS Client")
	fmt.Println("  分布式存储系统客户端")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("用法: lumeofs-client <命令> [选项]")
	fmt.Println()
	fmt.Println("可用命令:")
	fmt.Println("  put      上传文件到集群")
	fmt.Println("  get      从集群下载文件")
	fmt.Println("  list     列出目录内容")
	fmt.Println("  delete   删除文件")
	fmt.Println("  status   查看集群状态")
	fmt.Println("  version  显示版本信息")
	fmt.Println("  help     显示帮助信息")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  lumeofs-client put -file ./data.txt -dest /backup/data.txt")
	fmt.Println("  lumeofs-client get -path /backup/data.txt -output ./data.txt")
	fmt.Println("  lumeofs-client list -path /backup")
	fmt.Println("  lumeofs-client status")
}

// connectMaster 连接主节点
func connectMaster(addr string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("连接主节点失败: %w", err)
	}
	return conn, nil
}

func handlePut(master, localPath, remotePath string) {
	fmt.Println("========================================")
	fmt.Println("  文件上传")
	fmt.Println("========================================")
	fmt.Printf("  本地文件: %s\n", localPath)
	fmt.Printf("  目标路径: %s\n", remotePath)
	fmt.Printf("  主节点: %s\n", master)
	fmt.Println("----------------------------------------")

	// 检查本地文件是否存在
	info, err := os.Stat(localPath)
	if err != nil {
		fmt.Printf("  [错误] 无法访问文件: %v\n", err)
		return
	}

	// 读取文件内容
	data, err := os.ReadFile(localPath)
	if err != nil {
		fmt.Printf("  [错误] 读取文件失败: %v\n", err)
		return
	}

	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:])

	fmt.Printf("  文件大小: %d 字节\n", info.Size())
	fmt.Printf("  校验和: %s...\n", checksum[:16])
	fmt.Println("----------------------------------------")

	// 步骤1: 连接主节点并注册文件
	fmt.Println("  [1/4] 注册文件元数据...")
	conn, err := connectMaster(master)
	if err != nil {
		fmt.Printf("  [错误] %v\n", err)
		return
	}

	req := protocol.PutFileRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("req_%d", time.Now().UnixNano()),
		},
		FileName: filepath.Base(localPath),
		FilePath: remotePath,
		FileSize: info.Size(),
		Checksum: checksum,
	}

	payload, _ := protocol.EncodePayload(req)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypePutFile, Payload: payload})

	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		conn.Close()
		fmt.Printf("  [错误] 读取响应失败: %v\n", err)
		return
	}

	var putResp protocol.PutFileResponse
	protocol.DecodePayload(respMsg.Payload, &putResp)
	if !putResp.Success {
		conn.Close()
		fmt.Printf("  [错误] 注册失败: %s\n", putResp.Error)
		return
	}
	fmt.Printf("  [√] 文件ID: %s\n", putResp.FileID)

	// 步骤2: 请求分配数据块
	fmt.Println("  [2/4] 分配数据块...")
	allocReq := protocol.AllocChunkRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("alloc_%d", time.Now().UnixNano()),
		},
		FileID:   putResp.FileID,
		FileSize: info.Size(),
	}

	payload, _ = protocol.EncodePayload(allocReq)
	protocol.WriteMessage(conn, &protocol.Message{Type: protocol.MsgTypeAllocChunk, Payload: payload})

	respMsg, err = protocol.ReadMessage(conn)
	conn.Close() // 关闭与主节点的连接
	if err != nil {
		fmt.Printf("  [错误] 读取分配响应失败: %v\n", err)
		return
	}

	var allocResp protocol.AllocChunkResponse
	protocol.DecodePayload(respMsg.Payload, &allocResp)
	if !allocResp.Success {
		fmt.Printf("  [错误] 分配失败: %s\n", allocResp.Error)
		return
	}
	fmt.Printf("  [√] 分配了 %d 个数据块\n", len(allocResp.Chunks))

	// 步骤3: 将数据写入数据节点
	fmt.Println("  [3/4] 写入数据块...")
	successCount := 0
	for _, chunk := range allocResp.Chunks {
		// 计算该块的数据范围
		start := int64(chunk.Index) * (64 * 1024 * 1024)
		end := start + chunk.Size
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		chunkData := data[start:end]

		chunkHash := sha256.Sum256(chunkData)
		chunkChecksum := hex.EncodeToString(chunkHash[:])

		// 写入主副本节点
		if len(chunk.Nodes) > 0 {
			nodeAddr := chunk.Primary
			if nodeAddr == "0.0.0.0" || nodeAddr[:7] == "0.0.0.0" {
				nodeAddr = "127.0.0.1" + nodeAddr[7:]
			}

			nodeConn, err := net.DialTimeout("tcp", nodeAddr, 5*time.Second)
			if err != nil {
				fmt.Printf("    连接节点 %s 失败: %v\n", nodeAddr, err)
				continue
			}

			writeReq := protocol.WriteChunkRequest{
				Request: protocol.Request{
					RequestID: fmt.Sprintf("write_%d", time.Now().UnixNano()),
				},
				ChunkID:  chunk.ChunkID,
				Data:     chunkData,
				Offset:   0,
				Checksum: chunkChecksum,
			}

			payload, _ = protocol.EncodePayload(writeReq)
			protocol.WriteMessage(nodeConn, &protocol.Message{Type: protocol.MsgTypeWriteChunk, Payload: payload})

			writeRespMsg, err := protocol.ReadMessage(nodeConn)
			nodeConn.Close()

			if err == nil {
				var writeResp protocol.WriteChunkResponse
				protocol.DecodePayload(writeRespMsg.Payload, &writeResp)
				if writeResp.Success {
					successCount++
					fmt.Printf("    块[%d] 写入成功 (%d bytes)\n", chunk.Index, len(chunkData))
				}
			}
		}
	}

	fmt.Println("  [4/4] 完成")
	fmt.Println("----------------------------------------")
	if successCount == len(allocResp.Chunks) {
		fmt.Println("  [√] 上传成功!")
		fmt.Printf("  文件ID: %s\n", putResp.FileID)
		fmt.Printf("  数据块: %d\n", successCount)
	} else {
		fmt.Printf("  [‼] 部分成功: %d/%d 块\n", successCount, len(allocResp.Chunks))
	}
	fmt.Println("========================================")
}

func handleGet(master, remotePath, localPath string) {
	fmt.Println("========================================")
	fmt.Println("  文件下载")
	fmt.Println("========================================")
	fmt.Printf("  远程路径: %s\n", remotePath)
	fmt.Printf("  本地路径: %s\n", localPath)
	fmt.Printf("  主节点: %s\n", master)
	fmt.Println("----------------------------------------")

	// 连接主节点
	fmt.Println("  [1/2] 连接主节点...")
	conn, err := connectMaster(master)
	if err != nil {
		fmt.Printf("  [错误] %v\n", err)
		return
	}
	defer conn.Close()
	fmt.Println("  [√] 连接成功")

	// 发送下载请求
	fmt.Println("  [2/2] 发送下载请求...")
	req := protocol.GetFileRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("req_%d", time.Now().UnixNano()),
		},
		FilePath: remotePath,
	}

	payload, _ := protocol.EncodePayload(req)
	err = protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeGetFile,
		Payload: payload,
	})
	if err != nil {
		fmt.Printf("  [错误] 发送请求失败: %v\n", err)
		return
	}

	// 读取响应
	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		fmt.Printf("  [错误] 读取响应失败: %v\n", err)
		return
	}

	var resp protocol.GetFileResponse
	protocol.DecodePayload(respMsg.Payload, &resp)

	fmt.Println("----------------------------------------")
	if resp.Success {
		fmt.Println("  [√] 找到文件!")
		fmt.Printf("  文件名: %s\n", resp.FileName)
		fmt.Printf("  大小: %d 字节\n", resp.FileSize)
		fmt.Println("  (数据下载功能开发中)")
	} else {
		fmt.Printf("  [×] 下载失败: %s\n", resp.Error)
	}
	fmt.Println("========================================")
}

func handleList(master, path string) {
	fmt.Println("========================================")
	fmt.Println("  目录列表")
	fmt.Println("========================================")
	fmt.Printf("  路径: %s\n", path)
	fmt.Printf("  主节点: %s\n", master)
	fmt.Println("----------------------------------------")

	// 连接主节点
	conn, err := connectMaster(master)
	if err != nil {
		fmt.Printf("  [错误] %v\n", err)
		return
	}
	defer conn.Close()

	// 发送列目录请求
	type ListRequest struct {
		protocol.Request
		Path string `json:"path"`
	}

	req := ListRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("req_%d", time.Now().UnixNano()),
		},
		Path: path,
	}

	payload, _ := json.Marshal(req)
	err = protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeListDir,
		Payload: payload,
	})
	if err != nil {
		fmt.Printf("  [错误] 发送请求失败: %v\n", err)
		return
	}

	// 读取响应
	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		fmt.Printf("  [错误] 读取响应失败: %v\n", err)
		return
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

	var resp ListResponse
	json.Unmarshal(respMsg.Payload, &resp)

	if resp.Success {
		if len(resp.Files) == 0 {
			fmt.Println("  (目录为空)")
		} else {
			fmt.Printf("  共 %d 个文件:\n", len(resp.Files))
			fmt.Println("----------------------------------------")
			for _, f := range resp.Files {
				fmt.Printf("  %-30s %10d bytes\n", f.Name, f.Size)
			}
		}
	} else {
		fmt.Printf("  [×] 列表失败: %s\n", resp.Error)
	}
	fmt.Println("========================================")
}

func handleDelete(master, path string) {
	fmt.Println("========================================")
	fmt.Println("  删除文件")
	fmt.Println("========================================")
	fmt.Printf("  路径: %s\n", path)
	fmt.Printf("  主节点: %s\n", master)
	fmt.Println("----------------------------------------")
	fmt.Println("  删除功能开发中...")
	fmt.Println("========================================")
}

func handleStatus(master string) {
	fmt.Println("========================================")
	fmt.Println("  集群状态")
	fmt.Println("========================================")
	fmt.Printf("  主节点: %s\n", master)
	fmt.Println("----------------------------------------")

	// 连接主节点
	fmt.Println("  连接主节点...")
	conn, err := connectMaster(master)
	if err != nil {
		fmt.Printf("  [错误] %v\n", err)
		fmt.Println("========================================")
		return
	}
	defer conn.Close()
	fmt.Println("  [√] 连接成功")
	fmt.Println("----------------------------------------")

	// 发送状态查询请求
	req := protocol.StatusRequest{
		Request: protocol.Request{
			RequestID: fmt.Sprintf("req_%d", time.Now().UnixNano()),
		},
	}

	payload, _ := protocol.EncodePayload(req)
	err = protocol.WriteMessage(conn, &protocol.Message{
		Type:    protocol.MsgTypeGetStatus,
		Payload: payload,
	})
	if err != nil {
		fmt.Printf("  [错误] 发送请求失败: %v\n", err)
		return
	}

	// 读取响应
	respMsg, err := protocol.ReadMessage(conn)
	if err != nil {
		fmt.Printf("  [错误] 读取响应失败: %v\n", err)
		return
	}

	var resp protocol.StatusResponse
	protocol.DecodePayload(respMsg.Payload, &resp)

	if resp.Success {
		fmt.Printf("  数据节点数: %d\n", resp.NodeCount)
		fmt.Printf("  文件数量: %d\n", resp.FileCount)
		fmt.Printf("  数据块数: %d\n", resp.ChunkCount)
		fmt.Println("----------------------------------------")

		if len(resp.Nodes) > 0 {
			fmt.Println("  数据节点列表:")
			for _, n := range resp.Nodes {
				fmt.Printf("    - %s (%s) [%s]\n", n.NodeID, n.Address, n.Status)
			}
		} else {
			fmt.Println("  数据节点: (无)")
		}
	} else {
		fmt.Printf("  [×] 查询失败: %s\n", resp.Error)
	}
	fmt.Println("========================================")
}

// Package wal 实现Write-Ahead Log（预写日志）机制
// 写数据要在主写完操作log后才会刷盘
package wal

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EntryType WAL条目类型
type EntryType int

const (
	EntryTypeWrite  EntryType = iota // 写操作
	EntryTypeDelete                  // 删除操作
	EntryTypeSync                    // 同步操作
)

// Entry WAL日志条目
type Entry struct {
	Type      EntryType `json:"type"`
	ChunkID   string    `json:"chunk_id"`
	Data      []byte    `json:"data,omitempty"`
	Offset    int64     `json:"offset"`
	Version   int64     `json:"version"`
	Checksum  string    `json:"checksum"`
	Timestamp int64     `json:"timestamp"`
}

// WAL Write-Ahead Log 实现
type WAL struct {
	dir  string
	file *os.File
	mu   sync.Mutex

	// 当前日志序号
	sequence int64

	// 配置
	maxSize  int64 // 单个日志文件最大大小
	syncMode SyncMode
}

// SyncMode 同步模式
type SyncMode int

const (
	SyncImmediate SyncMode = iota // 每次写入立即同步
	SyncBatch                     // 批量同步
)

const (
	walFilePrefix  = "wal_"
	walFileSuffix  = ".log"
	defaultMaxSize = 64 * 1024 * 1024 // 64MB
	headerSize     = 8                // 条目头大小（长度字段）
)

// NewWAL 创建WAL实例
func NewWAL(dir string) (*WAL, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	w := &WAL{
		dir:      dir,
		maxSize:  defaultMaxSize,
		syncMode: SyncImmediate,
	}

	// 打开或创建当前日志文件
	if err := w.openCurrentFile(); err != nil {
		return nil, err
	}

	return w, nil
}

// openCurrentFile 打开当前日志文件
func (w *WAL) openCurrentFile() error {
	// 查找最新的日志文件
	files, err := filepath.Glob(filepath.Join(w.dir, walFilePrefix+"*"+walFileSuffix))
	if err != nil {
		return err
	}

	var currentFile string
	if len(files) == 0 {
		// 创建新文件
		w.sequence = 1
		currentFile = w.walFilePath(w.sequence)
	} else {
		// 使用最新的文件
		currentFile = files[len(files)-1]
		// 解析序号
		fmt.Sscanf(filepath.Base(currentFile), walFilePrefix+"%d"+walFileSuffix, &w.sequence)
	}

	file, err := os.OpenFile(currentFile, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.file = file

	return nil
}

// walFilePath 生成WAL文件路径
func (w *WAL) walFilePath(seq int64) string {
	return filepath.Join(w.dir, fmt.Sprintf("%s%08d%s", walFilePrefix, seq, walFileSuffix))
}

// Append 追加日志条目
func (w *WAL) Append(entry *Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 设置时间戳
	entry.Timestamp = time.Now().UnixNano()

	// 序列化条目
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	// 检查是否需要轮转文件
	if err := w.maybeRotate(int64(len(data) + headerSize)); err != nil {
		return err
	}

	// 写入条目长度（8字节）
	lengthBuf := make([]byte, headerSize)
	binary.BigEndian.PutUint64(lengthBuf, uint64(len(data)))
	if _, err := w.file.Write(lengthBuf); err != nil {
		return fmt.Errorf("failed to write entry length: %w", err)
	}

	// 写入条目数据
	if _, err := w.file.Write(data); err != nil {
		return fmt.Errorf("failed to write entry data: %w", err)
	}

	// 根据同步模式刷盘
	if w.syncMode == SyncImmediate {
		if err := w.file.Sync(); err != nil {
			return fmt.Errorf("failed to sync WAL: %w", err)
		}
	}

	return nil
}

// maybeRotate 检查是否需要轮转日志文件
func (w *WAL) maybeRotate(additionalSize int64) error {
	info, err := w.file.Stat()
	if err != nil {
		return err
	}

	if info.Size()+additionalSize > w.maxSize {
		// 关闭当前文件
		if err := w.file.Close(); err != nil {
			return err
		}

		// 创建新文件
		w.sequence++
		newFile, err := os.OpenFile(w.walFilePath(w.sequence),
			os.O_CREATE|os.O_RDWR|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		w.file = newFile
	}

	return nil
}

// ReadAll 读取所有日志条目
func (w *WAL) ReadAll() ([]*Entry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var entries []*Entry

	// 读取所有WAL文件
	files, err := filepath.Glob(filepath.Join(w.dir, walFilePrefix+"*"+walFileSuffix))
	if err != nil {
		return nil, err
	}

	for _, filePath := range files {
		fileEntries, err := w.readFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", filePath, err)
		}
		entries = append(entries, fileEntries...)
	}

	return entries, nil
}

// readFile 读取单个WAL文件
func (w *WAL) readFile(filePath string) ([]*Entry, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []*Entry
	lengthBuf := make([]byte, headerSize)

	for {
		// 读取条目长度
		_, err := io.ReadFull(file, lengthBuf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		length := binary.BigEndian.Uint64(lengthBuf)

		// 读取条目数据
		data := make([]byte, length)
		if _, err := io.ReadFull(file, data); err != nil {
			return nil, err
		}

		// 反序列化
		var entry Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, err
		}

		entries = append(entries, &entry)
	}

	return entries, nil
}

// Sync 强制同步到磁盘
func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.file.Sync()
}

// Close 关闭WAL
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		if err := w.file.Sync(); err != nil {
			return err
		}
		return w.file.Close()
	}
	return nil
}

// Truncate 截断已应用的日志（用于日志清理）
func (w *WAL) Truncate(beforeSeq int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	files, err := filepath.Glob(filepath.Join(w.dir, walFilePrefix+"*"+walFileSuffix))
	if err != nil {
		return err
	}

	for _, filePath := range files {
		var seq int64
		fmt.Sscanf(filepath.Base(filePath), walFilePrefix+"%d"+walFileSuffix, &seq)

		if seq < beforeSeq {
			if err := os.Remove(filePath); err != nil {
				return err
			}
		}
	}

	return nil
}

// SetSyncMode 设置同步模式
func (w *WAL) SetSyncMode(mode SyncMode) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.syncMode = mode
}

// SetMaxSize 设置单个日志文件最大大小
func (w *WAL) SetMaxSize(size int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.maxSize = size
}

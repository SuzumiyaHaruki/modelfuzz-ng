// Package persistence 提供长时间实验所需的原子 JSON 和追加式 JSONL 写入。
package persistence

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// WriteJSONAtomic 先同步临时文件，再通过同目录 rename 原子替换目标文件。
func WriteJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent directory for %s: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".modelfuzz-ng-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	if directory, err := os.Open(filepath.Dir(path)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func ReadJSON(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode %s: trailing JSON value", path)
		}
		return fmt.Errorf("decode %s trailing data: %w", path, err)
	}
	return nil
}

// Journal 每次 Append 都 Flush 和 fsync。吞吐优先的部署以后可以在上层按批
// 合并事件，但默认行为以进程崩溃后少丢数据为目标。
type Journal struct {
	mutex  sync.Mutex
	file   *os.File
	writer *bufio.Writer
}

func OpenJournal(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create journal directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open journal %s: %w", path, err)
	}
	if err := repairPartialLine(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("repair journal %s: %w", path, err)
	}
	return &Journal{file: file, writer: bufio.NewWriter(file)}, nil
}

// ReadLastJSONLine 读取最后一条完整 JSONL 记录。空文件返回 io.EOF。
func ReadLastJSONLine(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 16*1024*1024)
	var last []byte
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			last = append(last[:0], scanner.Bytes()...)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(last) == 0 {
		return io.EOF
	}
	return json.Unmarshal(last, destination)
}

func repairPartialLine(file *os.File) error {
	information, err := file.Stat()
	if err != nil || information.Size() == 0 {
		return err
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, information.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	position := information.Size()
	buffer := make([]byte, 4096)
	for position > 0 {
		start := position - int64(len(buffer))
		if start < 0 {
			start = 0
		}
		chunk := buffer[:position-start]
		if _, err := file.ReadAt(chunk, start); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for index := len(chunk) - 1; index >= 0; index-- {
			if chunk[index] == '\n' {
				return file.Truncate(start + int64(index) + 1)
			}
		}
		position = start
	}
	return file.Truncate(0)
}

func (j *Journal) Append(value any) error {
	if j == nil || j.file == nil {
		return fmt.Errorf("journal is closed")
	}
	j.mutex.Lock()
	defer j.mutex.Unlock()
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode journal event: %w", err)
	}
	if _, err := j.writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append journal event: %w", err)
	}
	if err := j.writer.Flush(); err != nil {
		return fmt.Errorf("flush journal: %w", err)
	}
	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("sync journal: %w", err)
	}
	return nil
}

func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	j.mutex.Lock()
	defer j.mutex.Unlock()
	if j.file == nil {
		return nil
	}
	flushErr := j.writer.Flush()
	syncErr := j.file.Sync()
	closeErr := j.file.Close()
	j.file = nil
	if flushErr != nil {
		return flushErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

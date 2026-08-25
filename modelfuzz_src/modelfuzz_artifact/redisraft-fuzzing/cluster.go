package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrRedisBug = errors.New("failed to terminate: redis bug encountered")
)

type RedisNodeConfig struct {
	ClusterID           int
	Port                int
	InterceptAddr       string
	InterceptListenAddr string
	NodeID              int
	WorkingDir          string
	RequestTimeout      int
	ElectionTimeout     int
	ModulePath          string
	BinaryPath          string
}

type RedisNode struct {
	ID      int
	logger  *Logger
	process *exec.Cmd
	client  *redis.Client
	config  *RedisNodeConfig
	ctx     context.Context
	cancel  func() error

	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func NewRedisNode(config *RedisNodeConfig, logger *Logger) *RedisNode {
	portS := strconv.Itoa(config.Port)
	addr := "localhost:" + portS

	return &RedisNode{
		ID:      config.NodeID,
		logger:  logger,
		process: nil,
		client: redis.NewClient(&redis.Options{
			Addr: addr,
		}),
		config: config,
		ctx:    nil,
		cancel: func() error { return nil },
		stdout: nil,
		stderr: nil,
	}
}

func (r *RedisNode) Create() {
	portS := strconv.Itoa(r.config.Port)
	addr := "localhost:" + portS

	serverArgs := []string{
		"--port", portS,
		"--bind", "0.0.0.0",
		"--dir", r.config.WorkingDir,
		"--dbfilename", fmt.Sprintf("redis%d.rdb", r.ID),
		"--loglevel", "debug",
		"--loadmodule", r.config.ModulePath,
		"--raft.addr", addr,
		"--raft.id", strconv.Itoa(r.ID),
		"--raft.log-filename", fmt.Sprintf("redis%d.db", r.ID),
		"--raft.use-test-network", "yes",
		"--raft.log-fsync", "no",
		"--raft.loglevel", "debug",
		"--raft.tls-enabled", "no",
		"--raft.trace", "off",
		"--raft.test-network-server-addr", r.config.InterceptAddr,
		"--raft.test-network-listen-addr", r.config.InterceptListenAddr,
		"--raft.request-timeout", strconv.Itoa(r.config.RequestTimeout),
		"--raft.election-timeout", strconv.Itoa(r.config.ElectionTimeout),
	}
	r.logger.With(LogParams{"server-args": strings.Join(serverArgs, " ")}).Debug("creating server")

	ctx, cancel := context.WithCancel(context.Background())
	r.process = exec.CommandContext(ctx, r.config.BinaryPath, serverArgs...)

	r.ctx = ctx
	r.cancel = func() error {
		err := r.process.Process.Signal(os.Interrupt)
		cancel()
		return err
	}
	if r.stdout == nil {
		r.stdout = new(bytes.Buffer)
	}
	if r.stderr == nil {
		r.stderr = new(bytes.Buffer)
	}
	r.process.Stdout = r.stdout
	r.process.Stderr = r.stderr
	r.process.Cancel = r.cancel
}

func (r *RedisNode) Start() error {
	if r.ctx != nil || r.process != nil {
		return errors.New("redis server already started")
	}

	r.Create()
	return r.process.Start()
}

func (r *RedisNode) Cleanup() {
	os.RemoveAll(r.config.WorkingDir)
}

func (r *RedisNode) Stop() error {
	if r.ctx == nil || r.process == nil {
		return errors.New("redis server not started")
	}
	select {
	case <-r.ctx.Done():
		return errors.New("redis server already stopped")
	default:
	}
	r.cancel()
	// Wait for process with a timeout
	done := make(chan error, 1)
	go func() {
		err := r.process.Wait()
		done <- err
	}()
	var err error = nil
	select {
	case <-time.After(50 * time.Millisecond):
		err = r.process.Process.Kill()
	case err = <-done:
	}

	r.ctx = nil
	r.cancel = func() error { return nil }
	r.process = nil

	return err
}

func (r *RedisNode) Terminate() error {
	r.Stop()
	r.Cleanup()

	stdout := strings.ToLower(r.stdout.String())
	stderr := strings.ToLower(r.stderr.String())

	if strings.Contains(stdout, "redis bug report") || strings.Contains(stderr, "redis bug report") {
		return ErrRedisBug
	}
	return nil
}

func (r *RedisNode) GetLogs() (string, string) {
	if r.stdout == nil || r.stderr == nil {
		return "", ""
	}
	return r.stdout.String(), r.stderr.String()
}

func (r *RedisNode) Execute(args ...string) error {
	if r.ctx == nil || r.process == nil {
		return errors.New("redis server not started")
	}
	select {
	case <-r.ctx.Done():
		return errors.New("redis server shutdown")
	default:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	argsI := make([]interface{}, len(args))
	for i, a := range args {
		argsI[i] = a
	}
	_, err := r.client.Do(ctx, argsI...).Result()
	return err
}

func (r *RedisNode) ExecuteAsync(args ...string) error {
	if r.ctx == nil || r.process == nil {
		return errors.New("redis server not started")
	}
	select {
	case <-r.ctx.Done():
		return errors.New("redis server shutdown")
	default:
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		argsI := make([]interface{}, len(args))
		for i, a := range args {
			argsI[i] = a
		}
		r.client.Do(ctx, argsI...).Result()
	}()
	return nil
}

func (r *RedisNode) cluster(args ...string) error {
	cmdArgs := []string{"RAFT.CLUSTER"}
	cmdArgs = append(cmdArgs, args...)

	return r.Execute(cmdArgs...)
}

type ClusterConfig struct {
	NumNodes              int
	RaftModulePath        string
	RedisServerBinaryPath string
	BasePort              int
	BaseInterceptPort     int
	ID                    int
	InterceptListenAddr   string
	WorkingDir            string
	LogLevel              string
	RequestTimeout        int
	ElectionTimeout       int
}

func (c *ClusterConfig) Copy() *ClusterConfig {
	return &ClusterConfig{
		NumNodes:              c.NumNodes,
		RaftModulePath:        c.RaftModulePath,
		RedisServerBinaryPath: c.RedisServerBinaryPath,
		BasePort:              c.BasePort,
		BaseInterceptPort:     c.BaseInterceptPort,
		ID:                    c.ID,
		InterceptListenAddr:   c.InterceptListenAddr,
		WorkingDir:            c.WorkingDir,
		LogLevel:              c.LogLevel,
		RequestTimeout:        c.RequestTimeout,
		ElectionTimeout:       c.ElectionTimeout,
	}
}

func (c *ClusterConfig) SetDefaults() {
	if c.RaftModulePath == "" {
		c.RaftModulePath = "/home/snagendra/Fuzzing/redisraft-fuzzing/redisraft.so"
	}
	if c.RedisServerBinaryPath == "" {
		c.RedisServerBinaryPath = "/home/snagendra/Fuzzing/redis/src/redis-server"
	}
	if c.WorkingDir == "" {
		c.WorkingDir = "/home/snagendra/Fuzzing/redisraft-fuzzing/tests/tmp"
	}
	if _, err := os.Stat(c.WorkingDir); err == nil {
		os.RemoveAll(c.WorkingDir)
	}
	os.MkdirAll(c.WorkingDir, 0777)

	if c.LogLevel == "" {
		c.LogLevel = "INFO"
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = 40
	}
	if c.ElectionTimeout == 0 {
		c.ElectionTimeout = 200
	}
}

func (c *ClusterConfig) GetNodeConfig(id int) *RedisNodeConfig {
	nodeWorkDir := path.Join(c.WorkingDir, strconv.Itoa(id))
	if _, err := os.Stat(nodeWorkDir); err == nil {
		os.RemoveAll(nodeWorkDir)
	}
	os.MkdirAll(nodeWorkDir, 0777)

	return &RedisNodeConfig{
		ClusterID:           c.ID,
		Port:                c.BasePort + id,
		InterceptAddr:       c.InterceptListenAddr,
		InterceptListenAddr: fmt.Sprintf("localhost:%d", c.BaseInterceptPort+id),
		NodeID:              id,
		WorkingDir:          nodeWorkDir,
		RequestTimeout:      c.RequestTimeout,
		ElectionTimeout:     c.ElectionTimeout,
		ModulePath:          c.RaftModulePath,
		BinaryPath:          c.RedisServerBinaryPath,
	}
}

type Cluster struct {
	Nodes  map[int]*RedisNode
	config *ClusterConfig
	logger *Logger
}

func NewCluster(config *ClusterConfig, logger *Logger) *Cluster {
	config.SetDefaults()
	c := &Cluster{
		config: config,
		Nodes:  make(map[int]*RedisNode),
		logger: logger,
	}
	// Make nodes
	for i := 1; i <= c.config.NumNodes; i++ {
		nConfig := config.GetNodeConfig(i)
		c.Nodes[i] = NewRedisNode(nConfig, c.logger.With(LogParams{"node": i}))
	}

	return c
}

func (c *Cluster) Start() error {
	primaryAddr := ""
	for i := 1; i <= c.config.NumNodes; i++ {
		node := c.Nodes[i]
		if err := node.Start(); err != nil {
			return fmt.Errorf("error starting node %d: %s", i, err)
		}
		if i == 1 {
			if err := node.cluster("init"); err != nil {
				return fmt.Errorf("failed to initialize: %s", err)
			}
			primaryAddr = "localhost:" + strconv.Itoa(node.config.Port)
		} else {
			c.logger.With(LogParams{"cluster": primaryAddr}).Debug("joining cluster")
			if err := node.cluster("join", primaryAddr); err != nil {
				return fmt.Errorf("failed to join cluster: %s", err)
			}
		}
	}
	return nil
}

func (c *Cluster) Destroy() error {
	var err error = nil
	for _, node := range c.Nodes {
		err = node.Terminate()
	}
	return err
}

func (c *Cluster) GetNode(id int) (*RedisNode, bool) {
	node, ok := c.Nodes[id]
	return node, ok
}

func (c *Cluster) GetLogs() string {
	logLines := []string{}
	for nodeID, node := range c.Nodes {
		logLines = append(logLines, fmt.Sprintf("logs for node: %d\n", nodeID))
		stdout, stderr := node.GetLogs()
		logLines = append(logLines, "----- Stdout -----", stdout, "----- Stderr -----", stderr, "\n\n")
	}
	return strings.Join(logLines, "\n")
}

func (c *Cluster) Execute(args ...string) error {
	node := c.Nodes[1]
	return node.Execute(args...)
}

func (c *Cluster) ExecuteAsync(args ...string) error {
	node := c.Nodes[1]
	return node.ExecuteAsync(args...)
}

package main

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testSeedPassword 是测试专用的预置账号口令。
// 按 AGENTS.md §5 第 4 条，演示口令的完整值只保留在 main.go，
// 测试代码一律使用与演示口令不同的测试专用口令。
const testSeedPassword = "TestOnly@2026"

// TestDemoPasswordDiffersFromTestSeedPassword 证明测试口令与演示口令不是同一个值。
func TestDemoPasswordDiffersFromTestSeedPassword(t *testing.T) {
	if demoPassword == "" {
		t.Fatal("演示口令不应为空")
	}
	if testSeedPassword == "" {
		t.Fatal("测试口令不应为空")
	}
	if testSeedPassword == demoPassword {
		t.Fatal("测试口令必须与演示口令不同")
	}
}

// freeAddr 返回一个已分配的本地动态端口地址（用于并行运行时避免端口冲突）。
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请动态端口失败: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestSeedCreatesPresetAccounts(t *testing.T) {
	addr := freeAddr(t)
	cfg := config{
		addr:            addr,
		dbPath:          filepath.Join(t.TempDir(), "server.db"),
		supplierURL:     "http://127.0.0.1:1",
		supplierSecret:  "secret",
		supplierTimeout: time.Second,
		clockScale:      1,
		sweepInterval:   time.Second,
		seedPassword:    testSeedPassword,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()

	// 等待服务就绪
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://" + addr + "/healthz")
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("服务未启动: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("健康检查应返回 200，实际 %d", resp.StatusCode)
	}
	// 预置账号使用测试口令登录成功
	body := strings.NewReader(`{"login":"demand-a","password":"` + testSeedPassword + `"}`)
	loginResp, err := http.Post("http://"+addr+"/api/login", "application/json", body)
	if err != nil {
		cancel()
		t.Fatalf("登录请求失败: %v", err)
	}
	_ = loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("测试口令登录应返回 200，实际 %d", loginResp.StatusCode)
	}
	// 演示口令不是本次预置的口令
	bodyDemo := strings.NewReader(`{"login":"demand-a","password":"` + demoPassword + `"}`)
	demoResp, err := http.Post("http://"+addr+"/api/login", "application/json", bodyDemo)
	if err != nil {
		cancel()
		t.Fatalf("登录请求失败: %v", err)
	}
	_ = demoResp.Body.Close()
	if demoResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("演示口令不应登录成功，实际 %d", demoResp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("正常退出不应报错: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("服务未在预期时间内退出")
	}
}

func TestSeedIsIdempotent(t *testing.T) {
	// 重复启动时账号不应重复插入失败（seed 内部已做存在性判断）。
	cfg := config{
		addr:           freeAddr(t),
		dbPath:         filepath.Join(t.TempDir(), "server.db"),
		supplierURL:    "http://127.0.0.1:1",
		supplierSecret: "secret",
		clockScale:     1,
		sweepInterval:  time.Second,
		seedPassword:   testSeedPassword,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()
	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done

	// 第二次使用同一个数据库启动，seed 不应报错
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- run(ctx2, cfg) }()
	time.Sleep(500 * time.Millisecond)
	cancel2()
	if err := <-done2; err != nil {
		t.Fatalf("重复启动不应报错: %v", err)
	}
}

func TestRunRejectsBadDatabasePath(t *testing.T) {
	// 数据库路径指向一个已存在的目录时，SQLite 无法打开，run 必须返回错误。
	dir := t.TempDir()
	cfg := config{
		addr:           freeAddr(t),
		dbPath:         dir,
		supplierURL:    "http://127.0.0.1:1",
		supplierSecret: "secret",
		clockScale:     1,
		sweepInterval:  time.Second,
		seedPassword:   testSeedPassword,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := run(ctx, cfg)
	if err == nil {
		t.Fatal("数据库路径不可用时应返回错误")
	}
}

func TestRunReportsListenError(t *testing.T) {
	// 端口被占用时，run 必须把监听错误返回给调用方。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer func() { _ = listener.Close() }()

	cfg := config{
		addr:           listener.Addr().String(),
		dbPath:         filepath.Join(t.TempDir(), "server.db"),
		supplierURL:    "http://127.0.0.1:1",
		supplierSecret: "secret",
		clockScale:     1,
		sweepInterval:  time.Second,
		seedPassword:   testSeedPassword,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cfg)
	if err == nil {
		t.Fatal("端口被占用时应返回错误")
	}
}

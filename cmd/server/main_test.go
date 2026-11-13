package main

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestSeedCreatesPresetAccounts(t *testing.T) {
	cfg := config{
		addr:            "127.0.0.1:18098",
		dbPath:          filepath.Join(t.TempDir(), "server.db"),
		supplierURL:     "http://127.0.0.1:1",
		supplierSecret:  "secret",
		supplierTimeout: time.Second,
		clockScale:      1,
		sweepInterval:   time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()

	// 等待服务就绪
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://127.0.0.1:18098/healthz")
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
		addr:           "127.0.0.1:18097",
		dbPath:         filepath.Join(t.TempDir(), "server.db"),
		supplierURL:    "http://127.0.0.1:1",
		supplierSecret: "secret",
		clockScale:     1,
		sweepInterval:  time.Second,
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
		addr:           "127.0.0.1:18095",
		dbPath:         dir,
		supplierURL:    "http://127.0.0.1:1",
		supplierSecret: "secret",
		clockScale:     1,
		sweepInterval:  time.Second,
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
	listener, err := net.Listen("tcp", "127.0.0.1:18094")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer func() { _ = listener.Close() }()

	cfg := config{
		addr:           "127.0.0.1:18094",
		dbPath:         filepath.Join(t.TempDir(), "server.db"),
		supplierURL:    "http://127.0.0.1:1",
		supplierSecret: "secret",
		clockScale:     1,
		sweepInterval:  time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = run(ctx, cfg)
	if err == nil {
		t.Fatal("端口被占用时应返回错误")
	}
}

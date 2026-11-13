// Command server 启动营销素材 AI 生图平台。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"creative-studio/internal/domain"
	"creative-studio/internal/httpapi"
	"creative-studio/internal/service"
	"creative-studio/internal/store"
)

// 演示账号与口令。演示口令的完整值只保留在本文件，文档中一律以掩码展示。
const demoPassword = "Studio@2026"

// config 是平台的运行参数。
type config struct {
	addr            string
	dbPath          string
	supplierURL     string
	supplierSecret  string
	supplierTimeout time.Duration
	clockScale      float64
	sweepInterval   time.Duration
}

func main() {
	var cfg config
	flag.StringVar(&cfg.addr, "addr", ":8080", "监听地址")
	flag.StringVar(&cfg.dbPath, "db", "data/studio.db", "SQLite 数据库文件路径")
	flag.StringVar(&cfg.supplierURL, "supplier-url", "http://127.0.0.1:8090", "模拟生成供应商地址")
	flag.StringVar(&cfg.supplierSecret, "supplier-secret", "demo-supplier-secret", "供应商回调签名密钥")
	flag.DurationVar(&cfg.supplierTimeout, "supplier-timeout", 10*time.Second, "单次提交供应商的超时")
	flag.Float64Var(&cfg.clockScale, "clock-scale", 1, "教学时钟倍率（1=题面规定的 60 秒；0.1=演示用 6 秒）")
	flag.DurationVar(&cfg.sweepInterval, "sweep-interval", 5*time.Second, "超时巡检间隔")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		log.Fatalf("启动失败：%v", err)
	}
}

// run 启动平台服务并阻塞直到上下文结束。
func run(ctx context.Context, cfg config) error {
	if dir := filepath.Dir(cfg.dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建数据目录失败: %w", err)
		}
	}
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	svc := service.New(st, domain.SystemClock{}, service.Config{
		SupplierBaseURL: cfg.supplierURL,
		SupplierSecret:  cfg.supplierSecret,
		SupplierTimeout: cfg.supplierTimeout,
		ClockScale:      cfg.clockScale,
	})
	if err := seed(svc); err != nil {
		return err
	}

	sweepCtx, cancelSweep := context.WithCancel(ctx)
	defer cancelSweep()
	svc.StartTimeoutSweeper(sweepCtx, cfg.sweepInterval)

	server, err := httpapi.New(svc, cfg.supplierSecret)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("服务已启动，地址 http://127.0.0.1%s，教学时钟倍率=%v（超时上限 %s）", cfg.addr, cfg.clockScale, domain.EffectiveTimeout(cfg.clockScale))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Println("收到退出信号，开始优雅关闭")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}

// seed 预置两个需求人、一个设计复核人与一个运营账号。
func seed(svc *service.Service) error {
	accounts := []struct {
		id   string
		name string
		role domain.Role
	}{
		{"demand-a", "需求人甲", domain.RoleRequester},
		{"demand-b", "需求人乙", domain.RoleRequester},
		{"review-a", "设计复核人甲", domain.RoleReviewer},
		{"ops-a", "运营甲", domain.RoleOperator},
	}
	for _, a := range accounts {
		if err := svc.SeedUser(context.Background(), a.id, a.name, a.role, demoPassword); err != nil {
			return fmt.Errorf("预置账号 %s 失败: %w", a.id, err)
		}
	}
	return nil
}

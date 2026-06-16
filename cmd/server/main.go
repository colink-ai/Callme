// Callme 智能客服系统服务入口
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"callme/internal/api"
	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"
	"callme/internal/service/auth"
	"callme/internal/service/feedback"
	"callme/internal/service/handoff"
	"callme/internal/service/session"
	"callme/internal/service/settings"
	"callme/internal/service/stats"
	"callme/internal/ws"

	// 注册 Agent 插件
	_ "callme/internal/service/agent/plugins/hermes"
	_ "callme/internal/service/agent/plugins/open_code"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// version 项目发布版本，构建时由 ldflags 注入（-X main.version=$(cat VERSION)）；
// 直接 go run 时为占位值。见 VERSION 文件与 scripts/package.sh。
var version = "dev"

// newLogger 创建日志器，并返回日志落点 io.Writer（供 gin 复用）。
//   - 配置了 log.path：日志只写文件（生产，后台运行不刷屏控制台）
//   - 未配置 log.path：日志写控制台 stderr（本地开发）
func newLogger(cfg config.LogConfig) (*zap.Logger, io.Writer, error) {
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoder := zapcore.NewJSONEncoder(encoderCfg)

	var sink io.Writer = os.Stderr
	if cfg.Path != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
			return nil, nil, fmt.Errorf("create log dir: %w", err)
		}
		sink = &lumberjack.Logger{
			Filename:   cfg.Path,
			MaxSize:    cfg.MaxSize, // MB
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge, // days
			Compress:   cfg.Compress,
		}
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(sink), zapcore.InfoLevel)
	return zap.New(core, zap.AddCaller()), sink, nil
}

func main() {
	configPath := flag.String("config", "configs/config.yaml", "配置文件路径")
	webDist := flag.String("web", "web/dist", "前端构建产物目录（空字符串禁用静态服务）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logger, logSink, err := newLogger(cfg.Log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	agent.SetLogger(logger)

	// Gin：生产模式（关闭 [GIN-debug] 控制台输出），并把 gin 的输出接到统一日志落点
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = logSink
	gin.DefaultErrorWriter = logSink

	db, err := repo.Open(cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		logger.Fatal("open database failed", zap.Error(err))
	}
	defer db.Close()
	logger.Info("Callme starting",
		zap.String("appVersion", version),
		zap.Int("schemaVersion", repo.SchemaVersion(db)))
	store := repo.NewStore(db)
	if err := store.CloseUnfinishedSessions(context.Background(), model.CloseReasonError); err != nil {
		logger.Warn("close unfinished sessions failed", zap.Error(err))
	}

	settingsSvc := settings.NewService(store, cfg.Agent, cfg.Session, logger)
	authSvc := auth.NewService(store, cfg.Auth.TokenTTL)
	sessionMgr := session.NewManager(cfg.Session, cfg.Agent, store, settingsSvc, func() []agent.MCPServerSpec { return nil }, logger)
	feedbackSvc := feedback.NewService(store, cfg.Feedback, cfg.Agent.HermesHome, logger)
	handoffSvc := handoff.NewService(store, cfg.Handoff, logger)
	statsSvc := stats.NewService(store, sessionMgr.Counts)
	wsHandler := ws.NewHandler(sessionMgr, authSvc, store, logger)

	dist := *webDist
	if dist != "" {
		if _, err := os.Stat(dist); err != nil {
			logger.Warn("web dist not found, static serving disabled", zap.String("path", dist))
			dist = ""
		}
	}

	router := api.NewRouter(&api.Deps{
		Store:    store,
		Sessions: sessionMgr,
		Settings: settingsSvc,
		Auth:     authSvc,
		Feedback: feedbackSvc,
		Handoff:  handoffSvc,
		Stats:    statsSvc,
		WS:       wsHandler,
		Logger:   logger,
		WebDist:  dist,
	})

	// 优雅退出：结束所有会话回收 Hermes 进程
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down, closing sessions...")
		sessionMgr.Shutdown()
		feedbackSvc.Shutdown()
		os.Exit(0)
	}()

	addr := cfg.Server.Addr()
	logger.Info("Callme server starting",
		zap.String("addr", addr),
		zap.String("agentType", cfg.Agent.Type),
		zap.Int("maxActive", settingsSvc.PoolSettings().MaxActive))
	if err := router.Run(addr); err != nil {
		logger.Fatal("server exited", zap.Error(err))
	}
}

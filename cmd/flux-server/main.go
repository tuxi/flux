package main

import (
	"flux/internal/config"
	"flux/pkg/cache"
	"flux/pkg/database"
	"flux/server"
	"log/slog"
	"os"
	"strconv"
)

//TIP <p>To run your code, right-click the code and select <b>Run</b>.</p> <p>Alternatively, click
// the <icon src="AllIcons.Actions.Execute"/> icon in the gutter and select the <b>Run</b> menu item from here.</p>

func main() {

	// 加载配置
	cfg, err := config.NewConfig("config.yaml")
	if err != nil {
		panic(err)
	}
	//
	//// lumberjack 是一个用于 Go 语言的轻量级日志轮转（Log Rotation）库
	//lumberLogger := &lumberjack.Logger{
	//	Filename:   "/var/log/dream-ai/app.log", // 日志文件路径
	//	MaxSize:    100,                         // 每个文件最大 100MB
	//	MaxBackups: 5,                           // 最多保留 5 个旧文件
	//	MaxAge:     30,                          // 最多保留 30 天
	//	Compress:   true,                        // 压缩旧文件
	//	LocalTime:  true,                        // 使用本地时间戳
	//}
	//logger.InitLogger(cfg.Log.Level, cfg.Log.Format, cfg.Log.AddSource, lumberLogger)

	slog.Info("flux 服务启动", "port", cfg.Server.Port)

	// 优先使用环境变量的配置
	dbCfg := cfg.Database
	dbUser := os.Getenv("DB_USER")
	dbPass := os.Getenv("DB_PASSWORD")
	dbHost := os.Getenv("DB_HOST")
	dbPort, _ := strconv.ParseInt(os.Getenv("DB_PORT"), 10, 64)
	dbName := os.Getenv("DB_NAME")
	if dbUser != "" && dbPass != "" && dbHost != "" {
		dbCfg.User = dbUser
		dbCfg.Password = dbPass
		dbCfg.Host = dbHost
		dbCfg.Port = int(dbPort)
		dbCfg.DBName = dbName
	}

	// 初始化数据库
	db, err := database.NewDatabase(&dbCfg)
	if err != nil {
		panic(err)
	}

	// 初始化redis缓存
	rds, err := cache.NewRedisCache(cfg.Redis)
	if err != nil {
		panic(err)
	}

	_ = server.NewServer(db.DB(), rds.GetClient(), cfg)
}

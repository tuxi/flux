package database

import (
	"flux/domain/entity"
	"flux/internal/config"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Database interface {
	DB() *gorm.DB
	CloseDB()
}

type defaultDatabase struct {
	db *gorm.DB
}

func NewDatabase(cfg *config.Database) (Database, error) {
	db, err := connect(cfg)
	if err != nil {
		return nil, err
	}
	return &defaultDatabase{db: db}, nil
}

func (d *defaultDatabase) DB() *gorm.DB {
	if d.db == nil {
		panic("数据库没有初始化，请先初始化它")
	}
	return d.db
}

func (d *defaultDatabase) CloseDB() {
	// 关闭主数据库
	if d.db != nil {
		db, _ := d.db.DB()
		if db != nil {
			_ = db.Close()
		}
	}
}

// connect 初始化并连接数据库
func connect(cfg *config.Database) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%v sslmode=%s TimeZone=Asia/Shanghai",
		cfg.Host, cfg.User, cfg.Password, cfg.DBName, cfg.Port, cfg.SSLMode)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		// 可以在这里关闭外键约束检查（如果迁移遇到循环依赖报错的话）
		// DisableForeignKeyConstraintWhenMigrating: true,
		// 建议开启日志，开发阶段能看到 SQL 语句，对复习 SQL 很有帮助
		//Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("数据库连接失败: %w", err)
	}

	// 获取底层 SQL 句柄，配置连接池
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// 设置最大的连接数
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 1. 初始化必备扩展 (针对 UUID 和 RAG 向量)
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\";").Error; err != nil {
		return nil, fmt.Errorf("无法加载 uuid 扩展: %w", err)
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector;").Error; err != nil {
		// 如果这里报错，说明你的 PG 镜像没安装 pgvector，面试时可以聊这个挑战
		fmt.Println("⚠️ 警告: vector 扩展加载失败，RAG 功能将受限")
	}

	// 2.自动迁移表结构
	err = db.AutoMigrate(
		&entity.TaskModel{},
		&entity.TaskEventModel{},
		&entity.TaskNodeModel{},
		&entity.TaskCostTraceModel{},
		&entity.AwaitBindingModel{},
		&entity.WorkflowModel{},
		&entity.WorkflowVersionModel{},
	)
	if err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	fmt.Println("✅ dream-AI 数据库初始化完成")
	return db, nil
}

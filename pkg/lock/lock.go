package lock

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// DistributedLock 分布式锁接口（适配Redis/DB）
type DistributedLock interface {
	// Lock 抢占锁，返回：是否成功、解锁函数、错误
	Lock(ctx context.Context, key string, timeout time.Duration) (bool, func(), error)
}

// RedisLock Redis实现（推荐生产环境用）
type RedisLock struct {
	rs *redsync.Redsync
}

func NewRedisLock(redisClient *redis.Client) *RedisLock {
	pool := goredis.NewPool(redisClient)
	return &RedisLock{
		rs: redsync.New(pool),
	}
}

// Lock 抢占分布式锁
func (l *RedisLock) Lock(ctx context.Context, key string, timeout time.Duration) (bool, func(), error) {
	mutex := l.rs.NewMutex(
		key,
		redsync.WithExpiry(timeout),
		redsync.WithTries(100), // 增加到 100 次
		redsync.WithRetryDelay(100*time.Millisecond), // 间隔缩小，反应更快
	)
	// 总等待能力 = 100 * 100ms = 10 秒

	// 抢占锁
	// 此时 mutex.LockContext 会阻塞直到拿到锁或重试耗尽
	if err := mutex.LockContext(ctx); err != nil {
		return false, nil, fmt.Errorf("lock failed: %w", err)
	}

	// 解锁函数
	unlock := func() {
		_, _ = mutex.UnlockContext(ctx)
	}

	return true, unlock, nil
}

// DBLock 基于DB的分布式锁（无Redis时用）
type DBLock struct {
	db *gorm.DB
}

func NewDBLock(db *gorm.DB) *DBLock {
	return &DBLock{db: db}
}

// Lock 基于DB的行锁实现
func (l *DBLock) Lock(ctx context.Context, key string, timeout time.Duration) (bool, func(), error) {
	// 1. 创建锁表（提前建表）
	// CREATE TABLE `distributed_lock` (
	//   `key` VARCHAR(255) NOT NULL PRIMARY KEY,
	//   `expire_at` DATETIME NOT NULL,
	//   `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
	// ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

	// 2. 抢占锁（INSERT ON DUPLICATE KEY UPDATE）
	now := time.Now()
	expireAt := now.Add(timeout)
	result := l.db.WithContext(ctx).
		Exec(`INSERT INTO distributed_lock (key, expire_at) 
			   VALUES (?, ?) 
			   ON DUPLICATE KEY UPDATE 
			   expire_at = IF(expire_at < NOW(), VALUES(expire_at), expire_at)`,
			key, expireAt)

	if result.Error != nil {
		return false, nil, result.Error
	}

	// 3. 检查是否抢占成功
	var count int64
	err := l.db.WithContext(ctx).
		Model(&struct{}{}).
		Where("`key` = ? AND expire_at = ?", key, expireAt).
		Count(&count).Error

	if err != nil {
		return false, nil, err
	}

	if count == 0 {
		return false, nil, nil
	}

	// 4. 解锁函数
	unlock := func() {
		_ = l.db.WithContext(ctx).
			Exec("DELETE FROM distributed_lock WHERE `key` = ? AND expire_at = ?", key, expireAt).Error
	}

	return true, unlock, nil
}

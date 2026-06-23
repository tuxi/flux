package cache

import (
	"context"
	"flux/internal/config"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	*redis.Client
}

// NewRedisCache 初始化redisClient
func NewRedisCache(cfg config.Redis) (*RedisCache, error) {
	redisClient := redis.NewClient(&redis.Options{
		DB:           cfg.DB,
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:     cfg.Password,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
	})

	// v9 强制要求使用 Context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := redisClient.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return &RedisCache{redisClient}, nil
}

func (c *RedisCache) GetClient() *redis.Client {
	return c.Client
}

// 关闭redis client
func (c *RedisCache) CloseRedis() {
	if nil != c.Client {
		_ = c.Close()
	}
}

package query

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisQueue 主要用于Worker 崩溃时，保障任务ID不丢，可以恢复执行中任务
type RedisQueue struct {
	client        *redis.Client
	queueKey      string
	processingKey string
	deadKey       string
	timeout       time.Duration
}

/*
NewRedisQueue
使用 Redis 的 BRPOPLPUSH 或 RPOPLPUSH 模式：
  - 主队列：video_task_queue（存放待执行任务）
  - 处理中队列：video_task_processing（存放正在处理的任务）
  - 执行流程：
    1. Worker 从 video_task_queue 阻塞弹出任务 → push 到 video_task_processing
    2. 执行任务
    3. 执行成功 → 从 video_task_processing 删除
    4. 执行失败 → 视重试策略可重新 push 回主队列
*/
func NewRedisQueue(
	client *redis.Client,
	queueKey,
	processingKey,
	deadKey string,
	timeout time.Duration,
) *RedisQueue {
	return &RedisQueue{
		client:        client,
		queueKey:      queueKey,
		processingKey: processingKey,
		deadKey:       deadKey,
		timeout:       timeout,
	}
}

// Push 往主队列添加任务
func (q *RedisQueue) Push(ctx context.Context, taskID int64) error {
	// q.client.LPush 添加到队列最左边，也就是最前面
	// q.client.RPush 添加到队列最右边
	return q.client.RPush(ctx, q.queueKey, taskID).Err()
}

/*
Lua 脚本执行redis
local task = redis.call("BRPOP", KEYS[1], 0) 阻塞弹出主队列，并返回 ["queueName", "taskID"]
redis.call("ZADD", KEYS[2], expireAt, taskID) 把任务加入
*/
var popAndReserveScript = redis.NewScript(`
local task = redis.call("BRPOP", KEYS[1], 0)
if not task then
    return nil
end

local taskID = task[2]
local expireAt = tonumber(ARGV[1])

redis.call("ZADD", KEYS[2], expireAt, taskID)

return taskID
`)

func (q *RedisQueue) PopAndReserve(ctx context.Context) (int64, error) {

	expireAt := time.Now().Add(q.timeout).Unix()

	result, err := popAndReserveScript.Run(ctx, q.client,
		[]string{q.queueKey, q.processingKey},
		expireAt,
	).Result()

	if err != nil {
		return 0, err
	}

	if result == nil {
		return 0, redis.Nil
	}
	taskID := result.(string)
	fmt.Printf("PopAndReserve Cache TaskID:%v Type:%T\n", result, result)
	id, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil {
		return 0, err
	}

	return id, nil
}

// Ack 删除 processing 队列任务（表示成功完成）
func (q *RedisQueue) Ack(ctx context.Context, taskID int64) error {
	return q.client.ZRem(ctx, q.processingKey, taskID).Err()
}

// Recover 将超时未完成任务重新推回主队列 只恢复“过期任务”
func (q *RedisQueue) Recover(ctx context.Context) error {

	now := time.Now().Unix()

	// 找到所有超时任务
	expiredTasks, err := q.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     q.processingKey,
		ByScore: true,
		Start:   "-inf",
		Stop:    fmt.Sprintf("%d", now),
	}).Result()

	if err != nil || len(expiredTasks) == 0 {
		return err
	}

	for _, taskID := range expiredTasks {

		// 原子移动
		pipe := q.client.TxPipeline()

		pipe.ZRem(ctx, q.processingKey, taskID)
		pipe.LPush(ctx, q.queueKey, taskID)

		_, err := pipe.Exec(ctx)
		if err != nil {
			log.Println("recover move failed:", err)
		}
	}

	return nil
}

// MoveToDead 重试3次失败，移动任务到到死信队列
func (q *RedisQueue) MoveToDead(ctx context.Context, taskID int64) error {
	pipe := q.client.TxPipeline()
	err := pipe.LPush(ctx, q.deadKey, taskID).Err()
	if err != nil {
		return err
	}
	err = pipe.Expire(ctx, q.deadKey, 7*24*time.Hour).Err() // 失败存储7天后才能重试
	if err != nil {
		return err
	}
	return nil
}

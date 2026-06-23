package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
)

type RedisStreamJobQueue struct {
	rdb    *redis.Client
	stream string
	group  string
}

func NewRedisStreamJobQueue(rdb *redis.Client, stream string, group string) *RedisStreamJobQueue {

	q := &RedisStreamJobQueue{
		rdb:    rdb,
		stream: stream,
		group:  group,
	}

	// 创建消费组
	_ = q.ensureGroup(context.Background())

	return q
}

func (q *RedisStreamJobQueue) Publish(ctx context.Context, job AsyncJob) error {
	if err := q.ensureGroup(ctx); err != nil {
		return err
	}

	data, _ := json.Marshal(job)

	return q.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		Values: map[string]any{
			"payload": string(data),
		},
	}).Err()
}

func (q *RedisStreamJobQueue) Consume(
	ctx context.Context,
	group string,
	consumer string,
) (*AsyncJob, string, error) {

	res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{q.stream, ">"},
		Count:    1,
		Block:    5 * 1000,
	}).Result()

	if err != nil {
		if isRedisNoGroupErr(err) {
			if ensureErr := q.ensureGroup(ctx); ensureErr != nil {
				return nil, "", ensureErr
			}
			return nil, "", redis.Nil
		}
		return nil, "", err
	}

	msg := res[0].Messages[0]

	payload := msg.Values["payload"].(string)

	var job AsyncJob
	_ = json.Unmarshal([]byte(payload), &job)

	return &job, msg.ID, nil
}

func (q *RedisStreamJobQueue) Ack(ctx context.Context, id string) error {

	return q.rdb.XAck(ctx, q.stream, q.group, id).Err()
}

func (q *RedisStreamJobQueue) ensureGroup(ctx context.Context) error {
	if q == nil || q.rdb == nil || q.stream == "" || q.group == "" {
		return nil
	}
	err := q.rdb.XGroupCreateMkStream(ctx, q.stream, q.group, "$").Err()
	if err == nil || strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

func isRedisNoGroupErr(err error) bool {
	return err != nil && !errors.Is(err, redis.Nil) && strings.Contains(err.Error(), "NOGROUP")
}

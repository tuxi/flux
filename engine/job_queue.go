package engine

import "context"

type AsyncJobQueue interface {

	// 发布任务
	Publish(ctx context.Context, job AsyncJob) error

	// Worker消费
	Consume(ctx context.Context, group string, consumer string) (*AsyncJob, string, error)

	// ack
	Ack(ctx context.Context, id string) error
}

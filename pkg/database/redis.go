// pkg/database/redis.go
package database

import (
	"context"

	"github.com/go-redis/redis/v8"
)

func NewRedisClient(ctx context.Context, addr, password string) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return client, nil
}

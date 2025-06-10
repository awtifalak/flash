package redis

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

type RedisRepository struct {
	client  *redis.Client
	timeout time.Duration
}

func NewRedisRepository(client *redis.Client, timeout time.Duration) *RedisRepository {
	return &RedisRepository{
		client:  client,
		timeout: timeout,
	}
}

// CreateReservation uses a Redis transaction to atomically reserve an item.
func (r *RedisRepository) CreateReservation(ctx context.Context, userID, itemID, code string) error {
	now := float64(time.Now().Unix())
	expireAt := now + r.timeout.Seconds()

	globalKey := "reservations:global"
	userKey := fmt.Sprintf("reservations:user:%s", userID)
	itemKey := fmt.Sprintf("item_reservation:%s", itemID)

	txf := func(tx *redis.Tx) error {
		// Check if item was reserved while we were processing
		if tx.Exists(ctx, itemKey).Val() == 1 {
			return errors.New("item already reserved")
		}
		// Check global and user limits
		if tx.ZCard(ctx, globalKey).Val() >= 10000 {
			return errors.New("sale completed, items sold out")
		}
		if tx.ZCard(ctx, userKey).Val() >= 10 {
			return errors.New("purchase limit exceeded for this user")
		}

		// Atomically execute reservation commands
		_, err := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.SetEX(ctx, itemKey, code, r.timeout)
			pipe.ZAdd(ctx, globalKey, &redis.Z{Score: expireAt, Member: code})
			pipe.ZAdd(ctx, userKey, &redis.Z{Score: expireAt, Member: code})
			pipe.Set(ctx, fmt.Sprintf("reservation:%s", code),
				fmt.Sprintf("%s|%s", userID, itemID),
				r.timeout)
			return nil
		})
		return err
	}

	// Retry transaction if itemKey is modified by another process
	for i := 0; i < 3; i++ {
		err := r.client.Watch(ctx, txf, itemKey)
		if err == nil {
			return nil // Success
		}
		if err == redis.TxFailedErr {
			continue // Conflict, retry
		}
		return err // Other error
	}
	return errors.New("item reservation failed after retries")
}

func (r *RedisRepository) GetReservation(ctx context.Context, code string) (string, string, error) {
	reservationKey := fmt.Sprintf("reservation:%s", code)
	val, err := r.client.Get(ctx, reservationKey).Result()
	if err == redis.Nil {
		return "", "", errors.New("Reservation not found or expired")
	} else if err != nil {
		return "", "", fmt.Errorf("redis error: %w", err)
	}

	parts := strings.Split(val, "|")
	if len(parts) != 2 {
		return "", "", errors.New("invalid reservation data format")
	}
	return parts[0], parts[1], nil
}

func (r *RedisRepository) DeleteReservation(ctx context.Context, userID, itemID, code string) error {
	keys := []string{
		fmt.Sprintf("item_reservation:%s", itemID),
		fmt.Sprintf("reservation:%s", code),
	}
	if err := r.client.Del(ctx, keys...).Err(); err != nil {
		return err
	}
	globalKey := "reservations:global"
	userKey := fmt.Sprintf("reservations:user:%s", userID)
	r.client.ZRem(ctx, globalKey, code)
	r.client.ZRem(ctx, userKey, code)
	return nil
}

// ResetAllReservations uses pipelining for slightly better performance.
func (r *RedisRepository) ResetAllReservations(ctx context.Context) error {
	patterns := []string{"reservations:user:*", "reservation:*", "item_reservation:*"}
	pipe := r.client.Pipeline()

	for _, pattern := range patterns {
		iter := r.client.Scan(ctx, 0, pattern, 0).Iterator()
		for iter.Next(ctx) {
			pipe.Del(ctx, iter.Val())
		}
		if err := iter.Err(); err != nil {
			return fmt.Errorf("error scanning keys for pattern %s: %w", pattern, err)
		}
	}
	pipe.Del(ctx, "reservations:global") // Also clear the global set

	_, err := pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return fmt.Errorf("error executing redis pipeline for reset: %w", err)
	}
	log.Println("All reservation keys in Redis have been reset.")
	return nil
}

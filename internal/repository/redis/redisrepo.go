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
	soldItemKey := fmt.Sprintf("item_sold:%s", itemID)
	userPurchaseCountKey := fmt.Sprintf("user_purchases:%s", userID)

	txf := func(tx *redis.Tx) error {
		// Check if item has already been sold permanently
		if tx.Exists(ctx, soldItemKey).Val() == 1 {
			return errors.New("item has already been sold")
		}

		// Check if item was reserved temporarily
		if tx.Exists(ctx, itemKey).Val() == 1 {
			return errors.New("item already reserved")
		}
		// Check global sale limit
		if tx.ZCard(ctx, globalKey).Val() >= 10000 {
			return errors.New("sale completed, items sold out")
		}

		// Check total purchase limit for the user
		// Note: .Int64() returns 0 if key doesn't exist, which is the desired behavior.
		purchasedCount, _ := tx.Get(ctx, userPurchaseCountKey).Int64()
		if purchasedCount >= 10 {
			return errors.New("purchase limit of 10 items exceeded for this user")
		}

		// Check concurrent reservation limit for the user
		if tx.ZCard(ctx, userKey).Val() >= 10 {
			return errors.New("concurrent reservation limit exceeded for this user")
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

	// Retry transaction if any of the watched keys are modified by another process
	for i := 0; i < 3; i++ {
		// Watch all keys that are read before the transaction is executed
		err := r.client.Watch(ctx, txf, itemKey, soldItemKey, userPurchaseCountKey)
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

// MarkItemAsSold sets a permanent key in Redis to mark an item as sold.
func (r *RedisRepository) MarkItemAsSold(ctx context.Context, itemID string) error {
	soldItemKey := fmt.Sprintf("item_sold:%s", itemID)
	// Set without expiration (0)
	return r.client.Set(ctx, soldItemKey, "sold", 0).Err()
}

// IncrementUserPurchaseCount increments the total number of items a user has purchased.
func (r *RedisRepository) IncrementUserPurchaseCount(ctx context.Context, userID string) (int64, error) {
	userPurchaseCountKey := fmt.Sprintf("user_purchases:%s", userID)
	return r.client.Incr(ctx, userPurchaseCountKey).Result()
}

// ResetAllReservations uses pipelining for slightly better performance.
// Note: This does NOT reset permanent keys like `item_sold` or `user_purchases`.
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
	log.Println("All temporary reservation keys in Redis have been reset.")
	return nil
}

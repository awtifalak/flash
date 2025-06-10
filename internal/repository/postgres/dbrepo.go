package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) SaveCheckoutAttempt(ctx context.Context, userID, itemID, code string) error {
	sql := `INSERT INTO checkout_attempts (user_id, item_id, code) VALUES ($1, $2, $3)`
	_, err := r.db.Exec(ctx, sql, userID, itemID, code)
	return err
}

func (r *PostgresRepository) ProcessPurchase(ctx context.Context, userID, itemID, code string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("transaction begin error: %w", err)
	}
	defer tx.Rollback(ctx)

	sqlInsertSale := `INSERT INTO sales (user_id, item_id, status) VALUES ($1, $2, 'pending')`
	if _, err := tx.Exec(ctx, sqlInsertSale, userID, itemID); err != nil {
		return fmt.Errorf("sales insert error: %w", err)
	}

	sqlUpdateCheckout := `UPDATE checkout_attempts SET used = true WHERE code = $1`
	if _, err := tx.Exec(ctx, sqlUpdateCheckout, code); err != nil {
		return fmt.Errorf("checkout update error: %w", err)
	}

	return tx.Commit(ctx)
}

func (r *PostgresRepository) FinalizeSales(ctx context.Context) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("transaction begin error: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now()
	prevHourStart := now.Truncate(time.Hour).Add(-time.Hour)
	prevHourEnd := prevHourStart.Add(time.Hour)

	var pendingCount int
	sqlCount := `SELECT COUNT(*) FROM sales WHERE status = 'pending' AND purchased_at >= $1 AND purchased_at < $2`
	if err := tx.QueryRow(ctx, sqlCount, prevHourStart, prevHourEnd).Scan(&pendingCount); err != nil {
		return 0, fmt.Errorf("pending sales count error: %w", err)
	}

	if pendingCount == 10000 {
		sqlConfirm := `UPDATE sales SET status = 'confirmed', committed_at = now() WHERE status = 'pending' AND purchased_at >= $1 AND purchased_at < $2`
		if _, err := tx.Exec(ctx, sqlConfirm, prevHourStart, prevHourEnd); err != nil {
			return 0, fmt.Errorf("sales confirmation error: %w", err)
		}
	} else {
		sqlDelete := `DELETE FROM sales WHERE status = 'pending' AND purchased_at >= $1 AND purchased_at < $2`
		if _, err := tx.Exec(ctx, sqlDelete, prevHourStart, prevHourEnd); err != nil {
			return 0, fmt.Errorf("pending sales deletion error: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("transaction commit error: %w", err)
	}

	return pendingCount, nil
}

func InitDB(ctx context.Context, dbPool *pgxpool.Pool) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS checkout_attempts (
			id SERIAL PRIMARY KEY, user_id TEXT NOT NULL, item_id TEXT NOT NULL,
			code TEXT NOT NULL UNIQUE, created_at TIMESTAMP DEFAULT NOW(), used BOOLEAN DEFAULT FALSE
		)`,
		`CREATE TABLE IF NOT EXISTS sales (
			id SERIAL PRIMARY KEY, user_id TEXT NOT NULL, item_id TEXT NOT NULL, status VARCHAR(20) NOT NULL,
			purchased_at TIMESTAMP DEFAULT NOW(), committed_at TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS sales_status_idx ON sales(status)`,
		`CREATE INDEX IF NOT EXISTS sales_purchased_idx ON sales(purchased_at)`,
	}
	for _, q := range queries {
		if _, err := dbPool.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

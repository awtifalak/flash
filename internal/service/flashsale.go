package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// Interfaces for repositories to allow for easy mocking and swapping implementations
type PostgresRepository interface {
	SaveCheckoutAttempt(ctx context.Context, userID, itemID, code string) error
	ProcessPurchase(ctx context.Context, userID, itemID, code string) error
	FinalizeSales(ctx context.Context) (int, error)
}

type RedisRepository interface {
	CreateReservation(ctx context.Context, userID, itemID, code string) error
	GetReservation(ctx context.Context, code string) (string, string, error)
	DeleteReservation(ctx context.Context, userID, itemID, code string) error
	ResetAllReservations(ctx context.Context) error
	MarkItemAsSold(ctx context.Context, itemID string) error
	IncrementUserPurchaseCount(ctx context.Context, userID string) (int64, error)
}

// PurchaseResult is a struct to hold data from a successful purchase
type PurchaseResult struct {
	UserID string
	ItemID string
}

type FlashSaleService struct {
	pgRepo    PostgresRepository
	redisRepo RedisRepository
	status    *Status
}

func NewFlashSaleService(pgRepo PostgresRepository, redisRepo RedisRepository) *FlashSaleService {
	return &FlashSaleService{
		pgRepo:    pgRepo,
		redisRepo: redisRepo,
		status:    NewStatus(),
	}
}

func (s *FlashSaleService) GetCurrentStatus() *Status {
	return s.status
}

func (s *FlashSaleService) CreateReservation(ctx context.Context, userID, itemID string) (string, error) {
	if s.status.IsSaleCompleted() {
		return "", fmt.Errorf("sale completed, items sold out")
	}

	code, err := generateUniqueCode()
	if err != nil {
		return "", fmt.Errorf("could not generate code: %w", err)
	}

	if err := s.redisRepo.CreateReservation(ctx, userID, itemID, code); err != nil {
		return "", err
	}

	if err := s.pgRepo.SaveCheckoutAttempt(ctx, userID, itemID, code); err != nil {
		// Attempt to roll back the Redis reservation if DB write fails
		_ = s.redisRepo.DeleteReservation(ctx, userID, itemID, code)
		return "", fmt.Errorf("failed to save checkout attempt: %w", err)
	}

	s.status.IncrementSuccessfulCheckouts()
	s.status.IncrementScheduledGoods()
	return code, nil
}

func (s *FlashSaleService) ProcessPurchase(ctx context.Context, code string) (*PurchaseResult, error) {
	userID, itemID, err := s.redisRepo.GetReservation(ctx, code)
	if err != nil {
		return nil, err
	}

	// The reservation is valid, now delete it from Redis
	if err := s.redisRepo.DeleteReservation(ctx, userID, itemID, code); err != nil {
		return nil, fmt.Errorf("failed to delete reservation: %w", err)
	}

	// Persist the purchase to the database
	if err := s.pgRepo.ProcessPurchase(ctx, userID, itemID, code); err != nil {
		return nil, fmt.Errorf("failed to process purchase in db: %w", err)
	}

	// After successful DB write, update Redis with permanent state
	// Mark the item as permanently sold
	if err := s.redisRepo.MarkItemAsSold(ctx, itemID); err != nil {
		// Log a critical error. The purchase is in the DB, but Redis state is inconsistent.
		// A background job could be used to fix such inconsistencies.
		log.Printf("CRITICAL: inconsistency detected. DB purchase for item %s succeeded, but failed to mark as sold in Redis: %v", itemID, err)
	}

	// Increment the user's total purchase count
	if _, err := s.redisRepo.IncrementUserPurchaseCount(ctx, userID); err != nil {
		log.Printf("CRITICAL: inconsistency detected. DB purchase for user %s succeeded, but failed to increment purchase count in Redis: %v", userID, err)
	}

	s.status.IncrementSuccessfulPurchases()
	s.status.IncrementPurchasedGoods()
	return &PurchaseResult{UserID: userID, ItemID: itemID}, nil
}

func (s *FlashSaleService) RunHourlyFinalization(ctx context.Context) {
	log.Println("Starting hourly sales finalization process...")
	for {
		now := time.Now()
		nextHour := now.Truncate(time.Hour).Add(time.Hour)
		waitDuration := time.Until(nextHour)

		select {
		case <-time.After(waitDuration):
			log.Println("Running sales finalization...")
			if err := s.finalizeSales(ctx); err != nil {
				log.Printf("Sales finalization error: %v", err)
			}
		case <-ctx.Done():
			log.Println("Stopping hourly finalization process.")
			return
		}
	}
}

func (s *FlashSaleService) finalizeSales(ctx context.Context) error {
	pendingCount, err := s.pgRepo.FinalizeSales(ctx)
	if err != nil {
		return fmt.Errorf("db finalization failed: %w", err)
	}

	if pendingCount == 10000 {
		log.Println("Sales confirmed - exactly 10000 orders")
		s.status.SetSaleCompleted(true)
	} else {
		log.Printf("Provisional sales count (%d) not equal to 10000. Sales canceled.", pendingCount)
		s.status.SetSaleCompleted(false)
	}

	// Reset metrics and clear Redis for the new sale hour
	s.status.Reset()

	if err := s.redisRepo.ResetAllReservations(ctx); err != nil {
		// Log error but don't fail the entire finalization. The system might recover.
		log.Printf("Redis reset error: %v", err)
	}

	return nil
}

func generateUniqueCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Status management struct
type Status struct {
	successfulCheckouts uint64
	failedCheckouts     uint64
	successfulPurchases uint64
	failedPurchases     uint64
	scheduledGoods      uint64
	purchasedGoods      uint64
	saleCompleted       uint32 // Atomic bool (0 or 1)
}

func NewStatus() *Status {
	return &Status{}
}

// ... Atomic getter and incrementer methods for Status ...
func (s *Status) IncrementSuccessfulCheckouts() { atomic.AddUint64(&s.successfulCheckouts, 1) }
func (s *Status) IncrementFailedCheckouts()     { atomic.AddUint64(&s.failedCheckouts, 1) }
func (s *Status) IncrementSuccessfulPurchases() { atomic.AddUint64(&s.successfulPurchases, 1) }
func (s *Status) IncrementFailedPurchases()     { atomic.AddUint64(&s.failedPurchases, 1) }
func (s *Status) IncrementScheduledGoods()      { atomic.AddUint64(&s.scheduledGoods, 1) }
func (s *Status) IncrementPurchasedGoods()      { atomic.AddUint64(&s.purchasedGoods, 1) }

func (s *Status) GetSuccessfulCheckouts() uint64 { return atomic.LoadUint64(&s.successfulCheckouts) }
func (s *Status) GetFailedCheckouts() uint64     { return atomic.LoadUint64(&s.failedCheckouts) }
func (s *Status) GetSuccessfulPurchases() uint64 { return atomic.LoadUint64(&s.successfulPurchases) }
func (s *Status) GetFailedPurchases() uint64     { return atomic.LoadUint64(&s.failedPurchases) }
func (s *Status) GetScheduledGoods() uint64      { return atomic.LoadUint64(&s.scheduledGoods) }
func (s *Status) GetPurchasedGoods() uint64      { return atomic.LoadUint64(&s.purchasedGoods) }

func (s *Status) IsSaleCompleted() bool { return atomic.LoadUint32(&s.saleCompleted) == 1 }
func (s *Status) SetSaleCompleted(val bool) {
	if val {
		atomic.StoreUint32(&s.saleCompleted, 1)
	} else {
		atomic.StoreUint32(&s.saleCompleted, 0)
	}
}

func (s *Status) SaleStatusText() string {
	if s.IsSaleCompleted() {
		return "completed"
	}
	return "active"
}

func (s *Status) Reset() {
	atomic.StoreUint64(&s.successfulCheckouts, 0)
	atomic.StoreUint64(&s.failedCheckouts, 0)
	atomic.StoreUint64(&s.successfulPurchases, 0)
	atomic.StoreUint64(&s.failedPurchases, 0)
	atomic.StoreUint64(&s.scheduledGoods, 0)
	atomic.StoreUint64(&s.purchasedGoods, 0)
	atomic.StoreUint32(&s.saleCompleted, 0)
}

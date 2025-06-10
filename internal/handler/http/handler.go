package http

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"flash/internal/service"
)

type FlashSaleService interface {
	CreateReservation(ctx context.Context, userID, itemID string) (string, error)
	ProcessPurchase(ctx context.Context, code string) (*service.PurchaseResult, error)
	GetCurrentStatus() *service.Status
	// Expose other service methods if needed
}

type Server struct {
	httpServer *http.Server
	service    FlashSaleService
}

func NewServer(addr string, svc FlashSaleService) (*Server, error) {
	mux := http.NewServeMux()
	server := &Server{
		service: svc,
	}

	mux.HandleFunc("/checkout", server.handleCheckout)
	mux.HandleFunc("/purchase", server.handlePurchase)
	mux.HandleFunc("/status", server.handleStatus)

	handlerWithMiddleware := recoverMiddleware(requestThrottlingMiddleware(2000, 5000)(mux))

	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      handlerWithMiddleware,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  15 * time.Second,
		},
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Handler implementations
func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	userID := r.URL.Query().Get("user_id")
	itemID := r.URL.Query().Get("id")
	if userID == "" || itemID == "" {
		s.service.GetCurrentStatus().IncrementFailedCheckouts()
		respondWithError(w, http.StatusBadRequest, "Missing user_id or id parameters")
		return
	}

	code, err := s.service.CreateReservation(r.Context(), userID, itemID)
	if err != nil {
		log.Printf("Reservation error: %v", err)
		s.service.GetCurrentStatus().IncrementFailedCheckouts()
		// Map service errors to HTTP status codes
		switch err.Error() {
		case ErrItemReserved, ErrSaleSoldOut, ErrItemAlreadySold, ErrPurchaseLimitExceeded, ErrConcurrentReservationExceeded:
			respondWithError(w, http.StatusBadRequest, err.Error())
		default:
			respondWithError(w, http.StatusInternalServerError, ErrInternalServer)
		}
		return
	}

	respondWithJSON(w, http.StatusOK, CheckoutResponse{
		Message: "success",
		Code:    code,
	})
}

func (s *Server) handlePurchase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondWithError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		s.service.GetCurrentStatus().IncrementFailedPurchases()
		respondWithError(w, http.StatusBadRequest, "Missing code parameter")
		return
	}

	result, err := s.service.ProcessPurchase(r.Context(), code)
	if err != nil {
		log.Printf("Purchase processing error: %v", err)
		s.service.GetCurrentStatus().IncrementFailedPurchases()
		if err.Error() == ErrReservationNotFound {
			respondWithError(w, http.StatusBadRequest, err.Error())
		} else {
			respondWithError(w, http.StatusInternalServerError, ErrInternalServer)
		}
		return
	}

	respondWithJSON(w, http.StatusOK, PurchaseResponse{
		Message: "success",
		User:    result.UserID,
		Item:    result.ItemID,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := s.service.GetCurrentStatus()
	now := time.Now()
	nextHour := now.Truncate(time.Hour).Add(time.Hour)

	respondWithJSON(w, http.StatusOK, StatusResponse{
		SecondsRemaining:    int(nextHour.Sub(now).Seconds()),
		SuccessfulCheckouts: status.GetSuccessfulCheckouts(),
		FailedCheckouts:     status.GetFailedCheckouts(),
		SuccessfulPurchases: status.GetSuccessfulPurchases(),
		FailedPurchases:     status.GetFailedPurchases(),
		ScheduledGoods:      status.GetScheduledGoods(),
		PurchasedGoods:      status.GetPurchasedGoods(),
		SaleStatus:          status.SaleStatusText(),
	})
}

// Helper and Middleware functions
func respondWithError(w http.ResponseWriter, status int, message string) {
	respondWithJSON(w, status, ErrorResponse{Error: message})
}

func respondWithJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("Handler panic: %v", err)
				respondWithError(w, http.StatusInternalServerError, ErrInternalServer)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestThrottlingMiddleware(limit int, burst int) func(http.Handler) http.Handler {
	tokenBucket := make(chan struct{}, burst)
	// Fill the bucket
	for i := 0; i < burst; i++ {
		tokenBucket <- struct{}{}
	}

	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(limit))
		defer ticker.Stop()
		for range ticker.C {
			select {
			case tokenBucket <- struct{}{}:
			default:
			}
		}
	}()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-tokenBucket:
				next.ServeHTTP(w, r)
			default:
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			}
		})
	}
}

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"flash/internal/config"
	"flash/internal/handler/http"
	"flash/internal/repository/postgres"
	"flash/internal/repository/redis"
	"flash/internal/service"
	"flash/pkg/database"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful Shutdown Setup
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("Received shutdown signal...")
		cancel()
	}()

	// Load Configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Setup Database & Redis Connections
	dbPool, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database connection error: %v", err)
	}
	defer dbPool.Close()

	redisClient, err := database.NewRedisClient(ctx, cfg.Redis.Addr, cfg.Redis.Password)
	if err != nil {
		log.Fatalf("Redis connection error: %v", err)
	}

	// Initialize Database Schema
	if err := postgres.InitDB(ctx, dbPool); err != nil {
		log.Fatalf("Database init error: %v", err)
	}

	// Dependency Injection: Create instances of repositories, services, and handlers
	pgRepo := postgres.NewPostgresRepository(dbPool)
	redisRepo := redis.NewRedisRepository(redisClient, cfg.ReservationTimeout)
	flashSaleSvc := service.NewFlashSaleService(pgRepo, redisRepo)

	// Start the background finalization process
	go flashSaleSvc.RunHourlyFinalization(ctx)

	// Setup and start the HTTP server
	addr := fmt.Sprintf(":%s", cfg.Port)
	server, err := http.NewServer(addr, flashSaleSvc)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	log.Printf("Starting HTTP server on %s", addr)
	if err := server.Start(ctx); err != nil {
		log.Printf("Server error: %v", err)
	}

	log.Println("Server stopped gracefully")
}

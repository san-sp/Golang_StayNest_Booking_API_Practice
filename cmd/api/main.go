package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"staynest/internal/auth"
	"staynest/internal/booking"
	"staynest/internal/config"
	"staynest/internal/customer"
	"staynest/internal/database"
	"staynest/internal/hotel"
	"staynest/internal/http/router"
	"staynest/internal/inventory"
	"staynest/internal/payment"
	"staynest/internal/payment/razorpay"
	"staynest/internal/room"
)

func main() {
	if err := run(); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// Load application configuration.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	// Use production mode when running in production.
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Establish the PostgreSQL connection pool.
	dbCtx, dbCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)

	pool, err := database.NewPool(dbCtx, cfg.DatabaseURL)
	dbCancel()

	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	tokenManager, err := auth.NewTokenManager(
		cfg.JWTSecret,
		15*time.Minute,
	)
	if err != nil {
		return fmt.Errorf("create token manager: %w", err)
	}

	// Initialize the HTTP router.
	customerRepo := customer.NewRepository(pool)
	customerService := customer.NewService(customerRepo, tokenManager)

	hotelRepo := hotel.NewRepository(pool)
	hotelService := hotel.NewService(hotelRepo)

	roomRepo := room.NewRepository(pool)
	roomService := room.NewService(roomRepo)

	inventoryRepo := inventory.NewRepository(pool)
	inventoryService := inventory.NewService(inventoryRepo)

	bookingRepo := booking.NewRepository(pool)
	bookingService := booking.NewService(bookingRepo)

	paymentRepo := payment.NewRepository(pool)

	razorpayClient, err := razorpay.NewClient(
		cfg.RazorpayKeyID,
		cfg.RazorpayKeySecret,
		nil,
	)
	if err != nil {
		return fmt.Errorf("create Razorpay client: %w", err)
	}

	refundService, err := payment.NewRefundService(
		paymentRepo,
		razorpayClient,
		10,
		60*time.Second,
		5*time.Minute,
	)
	if err != nil {
		return fmt.Errorf("create refund service: %w", err)
	}

	orderService, err := payment.NewOrderService(
		paymentRepo,
		razorpayClient,
		cfg.RazorpayKeyID,
	)
	if err != nil {
		return fmt.Errorf("create payment order service: %w", err)
	}

	paymentService, err := payment.NewService(
		paymentRepo,
		orderService,
		razorpayClient,
	)
	if err != nil {
		return fmt.Errorf("create payment service: %w", err)
	}

	webhookService, err := payment.NewWebhookService(
		paymentRepo,
		cfg.RazorpayWebhookSecret,
	)
	if err != nil {
		return fmt.Errorf("create payment webhook service: %w", err)
	}

	// Initialize the booking expiry worker.
	expiryWorker := booking.NewExpiryWorker(
		bookingRepo,
		booking.DefaultExpiryInterval,
		booking.DefaultExpiryBatchSize,
	)

	refundWorker := payment.NewRefundWorker(
		refundService,
		payment.DefaultRefundWorkerInterval,
	)

	// Give background workers a shared cancellable context.
	workerCtx, cancelWorker := context.WithCancel(context.Background())

	var workerWG sync.WaitGroup

	workerWG.Add(2)

	go func() {
		defer workerWG.Done()
		expiryWorker.Run(workerCtx)
	}()

	go func() {
		defer workerWG.Done()
		refundWorker.Run(workerCtx)
	}()

	// Ensure all background workers stop before the database pool closes.
	defer func() {
		cancelWorker()
		workerWG.Wait()
	}()

	r := router.New(
		pool,
		customerService,
		tokenManager,
		customerRepo,
		customerRepo,
		hotelService,
		roomService,
		inventoryService,
		bookingService,
		paymentService,
		webhookService,
	)

	// Configure the HTTP server.
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)

	// Start the HTTP server in a separate goroutine.
	go func() {
		slog.Info("starting StayNest HTTP server", "port", cfg.Port)

		err := server.ListenAndServe()

		if errors.Is(err, http.ErrServerClosed) {
			serverErrors <- nil
			return
		}

		serverErrors <- err
	}()

	// Wait for an operating-system shutdown signal.
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	select {
	case err := <-serverErrors:
		if err != nil {
			return fmt.Errorf("HTTP server: %w", err)
		}

	case <-ctx.Done():
		slog.Info("shutdown signal received")

		// Stop the background worker before shutting down the database.
		cancelWorker()
		workerWG.Wait()

		// Allow ongoing HTTP requests to finish.
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			server.Close()
			return fmt.Errorf("graceful shutdown: %w", err)
		}

		if err := <-serverErrors; err != nil {
			return fmt.Errorf("HTTP server shutdown: %w", err)
		}

		slog.Info("StayNest server stopped gracefully")
	}

	return nil
}

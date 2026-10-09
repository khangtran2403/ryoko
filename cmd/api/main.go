package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/admin_booking"
	"github.com/khangtran2403/ryoko/internal/amenities"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/config"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/handler"
	"github.com/khangtran2403/ryoko/internal/hotel"
	hotelimages "github.com/khangtran2403/ryoko/internal/hotel_images"
	"github.com/khangtran2403/ryoko/internal/inventory"
	"github.com/khangtran2403/ryoko/internal/mailer"
	"github.com/khangtran2403/ryoko/internal/middleware"
	"github.com/khangtran2403/ryoko/internal/oauth"
	"github.com/khangtran2403/ryoko/internal/passwordreset"
	"github.com/khangtran2403/ryoko/internal/review"
	"github.com/khangtran2403/ryoko/internal/roomtype"
	"github.com/khangtran2403/ryoko/internal/session"
)

const (
	serverReadHeaderTimeout  = 5 * time.Second
	serverReadTimeout        = 15 * time.Second
	serverWriteTimeout       = 30 * time.Second
	serverIdleTimeout        = 60 * time.Second
	serverMaxHeaderBytes     = 64 << 10
	databaseReadinessTimeout = 2 * time.Second
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	poolConfig, err := newDatabasePoolConfig(cfg.Database)
	if err != nil {
		log.Fatalf("invalid database pool configuration: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(appCtx, poolConfig)
	if err != nil {
		log.Fatalf("unable to create connection pool: %v", err)
	}
	defer pool.Close()
	// Fail fast if the DB isn't actually reachable.
	if err := pool.Ping(appCtx); err != nil {
		log.Fatalf("unable to reach database: %v", err)
	}

	queries := sqlc.New(pool)
	healthHandler := handler.NewHealthHandler(pool, databaseReadinessTimeout, slog.Default())
	tokenManager, err := auth.NewTokenManager(
		cfg.JWT.Secret,
		"ryoko",
		"ryoko-api",
		cfg.JWT.AccessTTL,
	)
	if err != nil {
		log.Fatalf("invalid token configuration: %v", err)
	}
	hotelService := hotel.NewService(queries)
	hotelHandler := handler.NewHotelHandler(queries, hotelService)
	roomTypeService := roomtype.NewService(pool, queries)
	roomTypeHandler := handler.NewRoomTypeHandler(queries, roomTypeService)
	amenityService := amenities.NewService(queries)
	amenityHandler := handler.NewAmenityHandler(amenityService)
	userHandler := handler.NewUserHandler(queries)
	sessionService, err := session.NewService(pool, queries, tokenManager, cfg.JWT.RefreshTTL)
	if err != nil {
		log.Fatalf("create session service: %v", err)
	}
	refreshCookies := session.NewRefreshCookieManager(cfg.JWT.RefreshCookieSecure)
	authHandler := handler.NewAuthHandler(queries, sessionService, refreshCookies)
	googleIDTokenValidator := oauth.NewGoogleIDTokenValidator(nil)
	googleProvider, err := oauth.NewGoogleProvider(
		cfg.GoogleOAuth.ClientID,
		cfg.GoogleOAuth.ClientSecret,
		cfg.GoogleOAuth.RedirectURL,
		googleIDTokenValidator,
	)
	if err != nil {
		log.Fatalf("create Google OAuth provider: %v", err)
	}
	oauthCookies, err := oauth.NewFlowCookieManager(
		cfg.OAuth.CookieSecret,
		cfg.OAuth.CookieSecure,
		cfg.OAuth.FlowTTL,
	)
	if err != nil {
		log.Fatalf("create OAuth flow cookie manager: %v", err)
	}
	oauthService, err := oauth.NewService(pool, queries, sessionService, cfg.OAuth.LoginCodeTTL)
	if err != nil {
		log.Fatalf("create OAuth service: %v", err)
	}
	oauthHandler, err := handler.NewOAuthHandler(
		googleProvider,
		oauthCookies,
		oauthService,
		refreshCookies,
		cfg.OAuth.SuccessRedirectURL,
	)
	if err != nil {
		log.Fatalf("create OAuth handler: %v", err)
	}
	emailSender, err := mailer.NewSMTPSender(mailer.SMTPConfig{
		Host:       cfg.SMTP.Host,
		Port:       cfg.SMTP.Port,
		Username:   cfg.SMTP.Username,
		Password:   cfg.SMTP.Password,
		From:       cfg.SMTP.From,
		RequireTLS: cfg.SMTP.RequireTLS,
		Timeout:    cfg.SMTP.Timeout,
	})
	if err != nil {
		log.Fatalf("create SMTP sender: %v", err)
	}
	passwordResetService, err := passwordreset.NewService(
		pool,
		queries,
		emailSender,
		cfg.PasswordReset.Pepper,
		cfg.PasswordReset.OTPTTL,
		cfg.PasswordReset.TokenTTL,
		cfg.PasswordReset.RequestCooldown,
	)
	if err != nil {
		log.Fatalf("create password reset service: %v", err)
	}
	passwordResetHandler := handler.NewPasswordResetHandler(passwordResetService, log.Default())
	passwordResetCleanupWorker := passwordreset.NewCleanupWorker(
		passwordResetService,
		cfg.PasswordReset.CleanupInterval,
		log.Default(),
	)
	authMiddleware := middleware.NewAuthMiddleware(tokenManager)
	corsMiddleware, err := middleware.NewCORSMiddleware(cfg.CORS.FrontendOrigin)
	if err != nil {
		log.Fatalf("create CORS middleware: %v", err)
	}
	authRateLimiter, err := middleware.NewIPRateLimiter(
		map[string]middleware.RateLimitPolicy{
			"register":               {Requests: 5, Window: time.Minute},
			"login":                  {Requests: 5, Window: time.Minute},
			"password-reset-request": {Requests: 3, Window: 15 * time.Minute},
			"password-reset-verify":  {Requests: 10, Window: 10 * time.Minute},
			"password-reset-confirm": {Requests: 10, Window: 10 * time.Minute},
			"oauth-exchange":         {Requests: 10, Window: time.Minute},
			"refresh":                {Requests: 30, Window: time.Minute},
		},
		5*time.Minute,
	)
	if err != nil {
		log.Fatalf("create authentication rate limiter: %v", err)
	}
	bookingService := booking.NewService(pool, queries)
	newAdminBookingService := admin_booking.NewService(queries)
	completionWorker := booking.NewCompletionWorker(
		bookingService,
		time.Hour,
		log.Default(),
	)
	sessionCleanupWorker := session.NewCleanupWorker(
		sessionService,
		cfg.JWT.CleanupInterval,
		log.Default(),
	)
	oauthCleanupWorker := oauth.NewCleanupWorker(
		oauthService,
		cfg.OAuth.CleanupInterval,
		log.Default(),
	)
	bookingHandler := handler.NewBookingHandler(bookingService)
	adminBookingHandler := handler.NewAdminBookingHandler(newAdminBookingService, bookingService)
	reviewService := review.NewService(queries)
	reviewHandler := handler.NewReviewHandler(reviewService)
	hotelImageService := hotelimages.NewService(pool, queries)
	hotelImageHandler := handler.NewHotelImageHandler(hotelImageService)
	inventoryService := inventory.NewService(pool, queries)
	inventoryHandler := handler.NewInventoryHandler(inventoryService)
	addr := ":" + strconv.Itoa(cfg.API.Port)

	apiHandler := newAPIHandler(apiDependencies{
		healthHandler:        healthHandler,
		hotelHandler:         hotelHandler,
		roomTypeHandler:      roomTypeHandler,
		amenityHandler:       amenityHandler,
		userHandler:          userHandler,
		authHandler:          authHandler,
		oauthHandler:         oauthHandler,
		passwordResetHandler: passwordResetHandler,
		bookingHandler:       bookingHandler,
		adminBookingHandler:  adminBookingHandler,
		reviewHandler:        reviewHandler,
		hotelImageHandler:    hotelImageHandler,
		inventoryHandler:     inventoryHandler,
		authMiddleware:       authMiddleware,
		corsMiddleware:       corsMiddleware,
		authRateLimiter:      authRateLimiter,
		logger:               slog.Default(),
	})
	server := newHTTPServer(addr, apiHandler)

	var workerWG sync.WaitGroup

	workerWG.Add(4)
	go func() {
		defer workerWG.Done()
		completionWorker.Run(appCtx)
	}()
	go func() {
		defer workerWG.Done()
		sessionCleanupWorker.Run(appCtx)
	}()
	go func() {
		defer workerWG.Done()
		passwordResetCleanupWorker.Run(appCtx)
	}()
	go func() {
		defer workerWG.Done()
		oauthCleanupWorker.Run(appCtx)
	}()

	serverErrors := make(chan error, 1)

	go func() {
		log.Printf("listening on %s", addr)
		serverErrors <- server.ListenAndServe()
	}()

	var serverErr error

	select {
	case <-appCtx.Done():
		log.Printf("shutdown signal received")

	case serverErr = <-serverErrors:
		// If the HTTP server stops unexpectedly, cancel the worker too.
		stop()
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful HTTP shutdown failed: %v", err)

		if closeErr := server.Close(); closeErr != nil {
			log.Printf("force HTTP close failed: %v", closeErr)
		}
	}

	workerWG.Wait()

	if serverErr != nil && !errors.Is(serverErr, http.ErrServerClosed) {
		log.Printf("HTTP server stopped unexpectedly: %v", serverErr)
	}

	log.Printf("server stopped")
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}
}

func newDatabasePoolConfig(database config.DatabaseConfig) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(database.URL)
	if err != nil {
		return nil, err
	}
	poolConfig.MaxConns = database.MaxConns
	poolConfig.MinConns = database.MinConns
	poolConfig.MaxConnLifetime = database.MaxConnLifetime
	poolConfig.MaxConnIdleTime = database.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = database.HealthCheckPeriod
	return poolConfig, nil
}

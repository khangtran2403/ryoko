package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/config"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/handler"
	hotelimages "github.com/khangtran2403/ryoko/internal/hotel_images"
	"github.com/khangtran2403/ryoko/internal/middleware"
	"github.com/khangtran2403/ryoko/internal/review"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(appCtx, cfg.Database.URL)
	if err != nil {
		log.Fatalf("Unable to create connection pool : %v", err)
	}
	defer pool.Close()
	// Fail fast if the DB isn't actually reachable.
	if err := pool.Ping(appCtx); err != nil {
		log.Fatalf("unable to reach database: %v", err)
	}

	queries := sqlc.New(pool)
	tokenManager, err := auth.NewTokenManager(
		cfg.JWT.Secret,
		"ryoko",
		"ryoko-api",
		15*time.Minute,
	)
	if err != nil {
		log.Fatalf("invalid token configuration: %v", err)
	}

	hotelHandler := handler.NewHotelHandler(queries)
	roomTypeHandler := handler.NewRoomTypeHandler(queries)
	amenityHandler := handler.NewAmenityHandler(queries)
	userHandler := handler.NewUserHandler(queries)
	authHandler := handler.NewAuthHandler(queries, tokenManager)
	authMiddleware := middleware.NewAuthMiddleware(tokenManager)
	bookingService := booking.NewService(pool, queries)
	completionWorker := booking.NewCompletionWorker(
		bookingService,
		time.Hour,
		log.Default(),
	)
	bookingHandler := handler.NewBookingHandler(bookingService)
	reviewSevice := review.NewService(queries)
	reviewHandler := handler.NewReviewHandler(reviewSevice)
	hotelImageService := hotelimages.NewService(pool, queries)
	hotelImageHandler := handler.NewHotelImageHandler(hotelImageService)
	adminOnly := func(handler http.HandlerFunc) http.Handler {
		return authMiddleware.Authenticate(
			middleware.RequireRole(
				auth.RoleAdmin,
				handler,
			),
		)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.Handle("POST /hotels", adminOnly(hotelHandler.Create))
	mux.HandleFunc("GET /hotels/{id}", hotelHandler.GetByID)
	mux.HandleFunc("GET /hotels", hotelHandler.ListHotelsByCity)
	mux.Handle("PUT /hotels/{id}", adminOnly(hotelHandler.UpdateHotel))
	mux.Handle("DELETE /hotels/{id}", adminOnly(hotelHandler.DeleteHotel))
	mux.Handle("POST /hotels/{hotelID}/room-types", adminOnly(roomTypeHandler.CreateRoomType))
	mux.HandleFunc("GET /room-types/{id}", roomTypeHandler.GetRoomTypeByID)
	mux.Handle("PUT /room-types/{id}", adminOnly(roomTypeHandler.UpdateRoomType))
	mux.Handle("DELETE /room-types/{id}", adminOnly(roomTypeHandler.DeleteRoomType))
	mux.HandleFunc("GET /hotels/{hotelID}/room-types", roomTypeHandler.ListRoomTypesByHotel)
	mux.Handle("POST /amenities", adminOnly(amenityHandler.CreateAmenity))
	mux.HandleFunc("GET /amenities", amenityHandler.ListAmenities)
	mux.Handle("POST /hotels/{hotelID}/amenities", adminOnly(amenityHandler.AddAmenityToHotel))
	mux.HandleFunc("GET /hotels/{hotelID}/amenities", amenityHandler.ListAmenitiesByHotel)
	mux.Handle("DELETE /hotels/{hotelID}/amenities/{amenityID}", adminOnly(amenityHandler.RemoveAmenitiesFromHotel))
	mux.Handle(
		"GET /me",
		authMiddleware.Authenticate(
			http.HandlerFunc(userHandler.GetMe),
		),
	)

	mux.Handle(
		"PUT /me",
		authMiddleware.Authenticate(
			http.HandlerFunc(userHandler.UpdateMe),
		),
	)

	mux.Handle(
		"DELETE /me",
		authMiddleware.Authenticate(
			http.HandlerFunc(userHandler.DeleteMe),
		),
	)
	mux.Handle("GET /me/bookings/{bookingID}", authMiddleware.Authenticate(
		http.HandlerFunc(bookingHandler.GetBookingByUserID),
	),
	)
	mux.Handle("GET /me/bookings", authMiddleware.Authenticate(
		http.HandlerFunc(bookingHandler.ListBookingsByUser),
	),
	)
	mux.Handle(
		"POST /room-types/{roomTypeID}/bookings",
		authMiddleware.Authenticate(
			http.HandlerFunc(bookingHandler.CreateBooking),
		),
	)
	mux.Handle("POST /me/bookings/{bookingID}/cancel",
		authMiddleware.Authenticate(
			http.HandlerFunc(bookingHandler.CancelBooking)))
	mux.Handle("POST /me/bookings/{bookingID}/review",
		authMiddleware.Authenticate(http.HandlerFunc(reviewHandler.CreateReview)))
	mux.HandleFunc("GET /reviews/{reviewID}", reviewHandler.GetReviewByID)
	mux.HandleFunc("GET /hotels/{hotelID}/reviews", reviewHandler.ListReviewByHotel)
	mux.Handle("PUT /me/reviews/{reviewID}",
		authMiddleware.Authenticate(http.HandlerFunc(reviewHandler.UpdateReviewByUser)))
	mux.Handle("DELETE /me/reviews/{reviewID}",
		authMiddleware.Authenticate(http.HandlerFunc(reviewHandler.DeleteReview)))
	mux.Handle(
		"POST /hotels/{hotelID}/images",
		adminOnly(hotelImageHandler.CreateHotelImage),
	)

	mux.HandleFunc(
		"GET /hotels/{hotelID}/images",
		hotelImageHandler.ListHotelImages,
	)

	mux.Handle(
		"PUT /hotels/{hotelID}/images/{imageID}/primary",
		adminOnly(hotelImageHandler.SetPrimaryHotelImage),
	)

	mux.Handle(
		"DELETE /hotels/{hotelID}/images/{imageID}",
		adminOnly(hotelImageHandler.DeleteHotelImage),
	)
	mux.HandleFunc("POST /auth/register", authHandler.RegisterUser)
	mux.HandleFunc("POST /auth/login", authHandler.LoginUser)

	addr := ":" + strconv.Itoa(cfg.API.Port)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	var workerWG sync.WaitGroup

	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		completionWorker.Run(appCtx)
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

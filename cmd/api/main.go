package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/config"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/handler"
	"github.com/khangtran2403/ryoko/internal/middleware"
	"github.com/khangtran2403/ryoko/internal/review"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), cfg.Database.URL)
	if err != nil {
		log.Fatalf("Unable to create connection pool : %v", err)
	}
	defer pool.Close()
	// Fail fast if the DB isn't actually reachable.
	if err := pool.Ping(context.Background()); err != nil {
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
	bookingHandler := handler.NewBookingHandler(bookingService)
	reviewSevice := review.NewService(queries)
	reviewHandler := handler.NewReviewHandler(reviewSevice)
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
	mux.HandleFunc("POST /auth/register", authHandler.RegisterUser)
	mux.HandleFunc("POST /auth/login", authHandler.LoginUser)

	addr := ":" + strconv.Itoa(cfg.API.Port)
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

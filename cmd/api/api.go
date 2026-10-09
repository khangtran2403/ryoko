package main

import (
	"log/slog"
	"net/http"

	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/handler"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

// apiDependencies contains the already-constructed HTTP-layer dependencies.
// Keeping construction outside this type lets main own infrastructure startup,
// while tests can exercise the same router and middleware stack as production.
type apiDependencies struct {
	healthHandler        *handler.HealthHandler
	hotelHandler         *handler.HotelHandler
	roomTypeHandler      *handler.RoomTypeHandler
	amenityHandler       *handler.AmenityHandler
	userHandler          *handler.UserHandler
	authHandler          *handler.AuthHandler
	oauthHandler         *handler.OAuthHandler
	passwordResetHandler *handler.PasswordResetHandler
	bookingHandler       *handler.BookingHandler
	adminBookingHandler  *handler.AdminBookingHandler
	reviewHandler        *handler.ReviewHandler
	hotelImageHandler    *handler.HotelImageHandler
	inventoryHandler     *handler.InventoryHandler
	authMiddleware       *middleware.AuthMiddleware
	corsMiddleware       *middleware.CORSMiddleware
	authRateLimiter      *middleware.IPRateLimiter
	logger               *slog.Logger
}

func newAPIHandler(deps apiDependencies) http.Handler {
	adminOnly := func(next http.HandlerFunc) http.Handler {
		return deps.authMiddleware.Authenticate(
			middleware.RequireRole(auth.RoleAdmin, next),
		)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", deps.healthHandler.Liveness)
	mux.HandleFunc("GET /health/ready", deps.healthHandler.Readiness)

	mux.Handle("POST /hotels", adminOnly(deps.hotelHandler.Create))
	mux.HandleFunc("GET /hotels/{id}", deps.hotelHandler.GetByID)
	mux.HandleFunc("GET /hotels", deps.hotelHandler.ListHotelsByCity)
	mux.Handle("PUT /hotels/{id}", adminOnly(deps.hotelHandler.UpdateHotel))
	mux.Handle("DELETE /hotels/{id}", adminOnly(deps.hotelHandler.DeleteHotel))
	mux.Handle("POST /hotels/{hotelID}/room-types", adminOnly(deps.roomTypeHandler.CreateRoomType))
	mux.HandleFunc("GET /room-types/{id}", deps.roomTypeHandler.GetRoomTypeByID)
	mux.Handle("PUT /room-types/{id}", adminOnly(deps.roomTypeHandler.UpdateRoomType))
	mux.Handle("DELETE /room-types/{id}", adminOnly(deps.roomTypeHandler.DeleteRoomType))
	mux.HandleFunc("GET /hotels/{hotelID}/room-types", deps.roomTypeHandler.ListRoomTypesByHotel)
	mux.Handle("POST /amenities", adminOnly(deps.amenityHandler.CreateAmenity))
	mux.Handle("PUT /amenities/{amenityID}", adminOnly(deps.amenityHandler.UpdateAmenity))
	mux.Handle("DELETE /amenities/{amenityID}", adminOnly(deps.amenityHandler.DeleteAmenity))
	mux.HandleFunc("GET /amenities", deps.amenityHandler.ListAmenities)
	mux.Handle("POST /hotels/{hotelID}/amenities", adminOnly(deps.amenityHandler.AddAmenityToHotel))
	mux.HandleFunc("GET /hotels/{hotelID}/amenities", deps.amenityHandler.ListAmenitiesByHotel)
	mux.Handle("DELETE /hotels/{hotelID}/amenities/{amenityID}", adminOnly(deps.amenityHandler.RemoveAmenitiesFromHotel))

	mux.Handle("GET /admin/bookings", adminOnly(deps.adminBookingHandler.ListBookingsForAdmin))
	mux.Handle("GET /admin/bookings/{bookingID}/history", adminOnly(deps.adminBookingHandler.ListBookingHistoryForAdmin))
	mux.Handle("POST /admin/bookings/{bookingID}/cancel", adminOnly(deps.adminBookingHandler.AdminCancellation))
	mux.Handle("PUT /admin/room-types/{roomTypeID}/blocked-inventory", adminOnly(deps.inventoryHandler.BlockedInventory))
	mux.Handle("GET /admin/room-types/{roomTypeID}/inventory", adminOnly(deps.inventoryHandler.ListRoomTypeInventory))

	mux.Handle("GET /me", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.userHandler.GetMe)))
	mux.Handle("PUT /me", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.userHandler.UpdateMe)))
	mux.Handle("DELETE /me", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.userHandler.DeleteMe)))
	mux.Handle("GET /me/bookings/{bookingID}", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.bookingHandler.GetBookingByUserID)))
	mux.Handle("GET /me/bookings", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.bookingHandler.ListBookingsByUser)))
	mux.Handle("GET /me/bookings/{bookingID}/history", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.bookingHandler.ListBookingStatusHistoryForUser)))
	mux.Handle("POST /room-types/{roomTypeID}/bookings", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.bookingHandler.CreateBooking)))
	mux.Handle("POST /me/bookings/{bookingID}/cancel", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.bookingHandler.CancelBooking)))
	mux.Handle("POST /me/bookings/{bookingID}/review", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.reviewHandler.CreateReview)))
	mux.Handle("PUT /me/reviews/{reviewID}", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.reviewHandler.UpdateReviewByUser)))
	mux.Handle("DELETE /me/reviews/{reviewID}", deps.authMiddleware.Authenticate(http.HandlerFunc(deps.reviewHandler.DeleteReview)))

	mux.HandleFunc("GET /reviews/{reviewID}", deps.reviewHandler.GetReviewByID)
	mux.HandleFunc("GET /hotels/{hotelID}/reviews", deps.reviewHandler.ListReviewByHotel)
	mux.Handle("POST /hotels/{hotelID}/images", adminOnly(deps.hotelImageHandler.CreateHotelImage))
	mux.HandleFunc("GET /hotels/{hotelID}/images", deps.hotelImageHandler.ListHotelImages)
	mux.Handle("PUT /hotels/{hotelID}/images/{imageID}/primary", adminOnly(deps.hotelImageHandler.SetPrimaryHotelImage))
	mux.Handle("DELETE /hotels/{hotelID}/images/{imageID}", adminOnly(deps.hotelImageHandler.DeleteHotelImage))
	mux.HandleFunc("GET /hotels/{hotelID}/available-room-types", deps.bookingHandler.ListAvailableRoomTypes)
	mux.HandleFunc("GET /hotels/search", deps.bookingHandler.SearchAvailableHotels)

	mux.Handle("POST /auth/register", deps.authRateLimiter.Limit("register", http.HandlerFunc(deps.authHandler.RegisterUser)))
	mux.Handle("POST /auth/login", deps.authRateLimiter.Limit("login", http.HandlerFunc(deps.authHandler.LoginUser)))
	mux.Handle("POST /auth/refresh", deps.corsMiddleware.RequireAllowedOrigin(
		deps.authRateLimiter.Limit("refresh", http.HandlerFunc(deps.authHandler.RefreshToken)),
	))
	mux.Handle("POST /auth/logout", deps.corsMiddleware.RequireAllowedOrigin(http.HandlerFunc(deps.authHandler.Logout)))
	mux.HandleFunc("GET /auth/google", deps.oauthHandler.StartGoogle)
	mux.HandleFunc("GET /auth/google/callback", deps.oauthHandler.GoogleCallback)
	mux.Handle("POST /auth/oauth/exchange", deps.authRateLimiter.Limit("oauth-exchange", http.HandlerFunc(deps.oauthHandler.ExchangeCode)))
	mux.Handle("POST /auth/password-reset/request", deps.authRateLimiter.Limit("password-reset-request", http.HandlerFunc(deps.passwordResetHandler.Request)))
	mux.Handle("POST /auth/password-reset/verify", deps.authRateLimiter.Limit("password-reset-verify", http.HandlerFunc(deps.passwordResetHandler.Verify)))
	mux.Handle("POST /auth/password-reset/confirm", deps.authRateLimiter.Limit("password-reset-confirm", http.HandlerFunc(deps.passwordResetHandler.Confirm)))

	return middleware.SecurityHeaders(
		middleware.RequestID(
			middleware.AccessLog(
				deps.logger,
				middleware.RecoverPanic(deps.logger, deps.corsMiddleware.Allow(mux)),
			),
		),
	)
}

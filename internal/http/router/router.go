package router

import (
	"time"

	"github.com/gin-gonic/gin"

	"staynest/internal/http/handler"
	"staynest/internal/http/middleware"
)

func New(
	db handler.DBPinger,
	customerService handler.CustomerService,
	tokenVerifier middleware.AccessTokenVerifier,
	activeCustomerChecker middleware.ActiveCustomerChecker,
	adminChecker middleware.AdminChecker,
	hotelService handler.HotelService,
	roomService handler.RoomService,
	inventoryService handler.InventoryService,
	bookingService handler.BookingService,
	paymentService handler.PaymentService,
	webhookService handler.PaymentWebhookService,
) *gin.Engine {
	r := gin.New()

	loginLimiter, err := middleware.NewIPRateLimiter(10, time.Minute)
	if err != nil {
		panic(err)
	}

	// Do not trust client-supplied forwarded IP headers.
	// Use the direct network peer address.
	if err := r.SetTrustedProxies(nil); err != nil {
		panic(err)
	}

	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.GET("/health", handler.Health)
	r.GET("/ready", handler.Ready(db))

	r.POST("/api/v1/auth/register", handler.RegisterCustomer(customerService))
	r.POST("/api/v1/auth/login", loginLimiter.Middleware(), handler.LoginCustomer(customerService))

	// Public hotel routes.
	r.GET("/api/v1/hotels", handler.ListHotels(hotelService))
	r.GET("/api/v1/hotels/search", handler.SearchHotels(hotelService))
	r.GET("/api/v1/hotels/:id", handler.GetHotel(hotelService))

	// Public room routes.
	r.GET("/api/v1/hotels/:id/rooms", handler.ListRoomTypesByHotelID(roomService))
	r.GET("/api/v1/rooms/:id", handler.GetRoomType(roomService))

	// Public inventory routes.
	r.GET("/api/v1/rooms/:id/inventory", handler.ListRoomInventory(inventoryService))

	// Public Razorpay webhook route.
	// This endpoint must not require customer authentication.
	r.POST(
		"/api/v1/webhooks/razorpay",
		handler.RazorpayWebhook(webhookService),
	)

	protected := r.Group("/api/v1")
	protected.Use(middleware.Authenticate(
		tokenVerifier,
		activeCustomerChecker,
	))

	protected.GET("/customers/me", handler.GetCustomerProfile(customerService))
	protected.PATCH("/customers/me", handler.UpdateCustomerProfile(customerService))
	protected.POST("/auth/change-password", handler.ChangeCustomerPassword(customerService))
	protected.DELETE("/customers/me", handler.DeactivateCustomer(customerService))

	// Customer booking routes.
	protected.GET("/bookings", handler.ListCustomerBookings(bookingService))
	protected.GET("/bookings/:id", handler.GetCustomerBooking(bookingService))
	protected.POST("/bookings", handler.CreateBooking(bookingService))
	protected.PATCH("/bookings/:id", handler.ModifyBooking(bookingService))
	protected.DELETE("/bookings/:id", handler.CancelBooking(bookingService))
	protected.POST("/bookings/:id/payments", handler.InitiatePayment(paymentService))
	protected.POST("/payments/verify", handler.VerifyPayment(paymentService))

	// Admin routes require authentication and administrator privileges.
	admin := protected.Group("/admin")
	admin.Use(middleware.RequireAdmin(adminChecker))

	admin.POST("/hotels", handler.CreateHotel(hotelService))
	admin.PATCH("/hotels/:id", handler.UpdateHotel(hotelService))

	// Admin room management routes.
	admin.POST("/hotels/:id/rooms", handler.CreateRoomType(roomService))
	admin.PATCH("/rooms/:id", handler.UpdateRoomType(roomService))

	// Admin inventory management.
	admin.PUT("/rooms/:id/inventory/:date", handler.SetDailyInventory(inventoryService))

	return r
}

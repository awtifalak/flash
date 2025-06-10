package http

// Custom error types for specific business logic failures
var (
	ErrItemReserved          = "item already reserved"
	ErrPurchaseLimitExceeded = "purchase limit exceeded for this user"
	ErrSaleSoldOut           = "sale completed, items sold out"
	ErrReservationNotFound   = "Reservation not found or expired"
	ErrInternalServer        = "Internal server error"
)

type CheckoutResponse struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type PurchaseResponse struct {
	Message string `json:"message"`
	User    string `json:"user"`
	Item    string `json:"item"`
}

type StatusResponse struct {
	SecondsRemaining    int    `json:"seconds_remaining"`
	SuccessfulCheckouts uint64 `json:"successful_checkouts"`
	FailedCheckouts     uint64 `json:"failed_checkouts"`
	SuccessfulPurchases uint64 `json:"successful_purchases"`
	FailedPurchases     uint64 `json:"failed_purchases"`
	ScheduledGoods      uint64 `json:"scheduled_goods"`
	PurchasedGoods      uint64 `json:"purchased_goods"`
	SaleStatus          string `json:"sale_status"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

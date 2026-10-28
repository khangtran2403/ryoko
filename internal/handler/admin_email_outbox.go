package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/khangtran2403/ryoko/internal/notifications"
)

type adminEmailOutboxService interface {
	ListEmails(context.Context, notification.ListAdminEmailsInput) (notification.ListAdminEmailsResult, error)
	RetryFailedEmail(context.Context, int64) (notification.AdminEmail, error)
}

type AdminEmailOutboxHandler struct {
	service adminEmailOutboxService
}

func NewAdminEmailOutboxHandler(service adminEmailOutboxService) *AdminEmailOutboxHandler {
	if service == nil {
		panic("admin email outbox service must not be nil")
	}
	return &AdminEmailOutboxHandler{service: service}
}

type AdminEmailOutboxResponse struct {
	ID             int64      `json:"id"`
	BookingID      int64      `json:"booking_id"`
	EventType      string     `json:"event_type"`
	RecipientEmail string     `json:"recipient_email"`
	Status         string     `json:"status"`
	Attempts       int32      `json:"attempts"`
	AvailableAt    time.Time  `json:"available_at"`
	LockedAt       *time.Time `json:"locked_at"`
	SentAt         *time.Time `json:"sent_at"`
	LastError      *string    `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type ListAdminEmailOutboxResponse struct {
	Emails     []AdminEmailOutboxResponse        `json:"emails"`
	Pagination notification.AdminEmailPagination `json:"pagination"`
}

func (h *AdminEmailOutboxHandler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, ok := positiveInt32Query(w, query.Get("page"), notification.DefaultAdminEmailPage, "page")
	if !ok {
		return
	}
	pageSize, ok := positiveInt32Query(
		w,
		query.Get("page_size"),
		notification.DefaultAdminEmailPageSize,
		"page_size",
	)
	if !ok {
		return
	}
	if pageSize > notification.MaxAdminEmailPageSize {
		http.Error(w, "page_size must be between 1 and 100", http.StatusBadRequest)
		return
	}

	var status *string
	if raw := strings.TrimSpace(query.Get("status")); raw != "" {
		normalized := strings.ToLower(raw)
		status = &normalized
	}

	result, err := h.service.ListEmails(r.Context(), notification.ListAdminEmailsInput{
		Status: status, Page: page, PageSize: pageSize,
	})
	if errors.Is(err, notification.ErrInvalidAdminEmailStatus) ||
		errors.Is(err, notification.ErrInvalidAdminEmailPage) ||
		errors.Is(err, notification.ErrInvalidAdminEmailPageSize) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "Failed to list email outbox messages", http.StatusInternalServerError)
		return
	}

	emails := make([]AdminEmailOutboxResponse, len(result.Emails))
	for index, email := range result.Emails {
		emails[index] = adminEmailOutboxResponse(email)
	}
	writeJSON(w, http.StatusOK, ListAdminEmailOutboxResponse{
		Emails: emails, Pagination: result.Pagination,
	})
}

func (h *AdminEmailOutboxHandler) Retry(w http.ResponseWriter, r *http.Request) {
	emailID, err := strconv.ParseInt(r.PathValue("emailID"), 10, 64)
	if err != nil || emailID <= 0 {
		http.Error(w, "Invalid email outbox ID", http.StatusBadRequest)
		return
	}

	email, err := h.service.RetryFailedEmail(r.Context(), emailID)
	switch {
	case errors.Is(err, notification.ErrAdminEmailNotFound):
		http.Error(w, "Email outbox message not found", http.StatusNotFound)
		return
	case errors.Is(err, notification.ErrAdminEmailNotFailed):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "Failed to retry email outbox message", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, adminEmailOutboxResponse(email))
}

func positiveInt32Query(
	w http.ResponseWriter,
	raw string,
	defaultValue int32,
	name string,
) (int32, bool) {
	if raw == "" {
		return defaultValue, true
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		http.Error(w, name+" must be a positive integer", http.StatusBadRequest)
		return 0, false
	}
	return int32(value), true
}

func adminEmailOutboxResponse(email notification.AdminEmail) AdminEmailOutboxResponse {
	return AdminEmailOutboxResponse{
		ID: email.ID, BookingID: email.BookingID, EventType: email.EventType,
		RecipientEmail: email.RecipientEmail, Status: email.Status, Attempts: email.Attempts,
		AvailableAt: email.AvailableAt.Time.UTC(),
		LockedAt:    optionalAdminEmailTime(email.LockedAt.Valid, email.LockedAt.Time),
		SentAt:      optionalAdminEmailTime(email.SentAt.Valid, email.SentAt.Time),
		LastError:   optionalAdminEmailText(email.LastError.Valid, email.LastError.String),
		CreatedAt:   email.CreatedAt.Time.UTC(), UpdatedAt: email.UpdatedAt.Time.UTC(),
	}
}

func optionalAdminEmailTime(valid bool, value time.Time) *time.Time {
	if !valid {
		return nil
	}
	value = value.UTC()
	return &value
}

func optionalAdminEmailText(valid bool, value string) *string {
	if !valid {
		return nil
	}
	return &value
}

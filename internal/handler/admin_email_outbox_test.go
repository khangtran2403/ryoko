package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	notifications "github.com/khangtran2403/ryoko/internal/notifications"
)

type fakeAdminEmailOutboxService struct {
	listInput  notifications.ListAdminEmailsInput
	listResult notifications.ListAdminEmailsResult
	listErr    error
	retryID    int64
	retryEmail notifications.AdminEmail
	retryErr   error
}

func (f *fakeAdminEmailOutboxService) ListEmails(
	_ context.Context,
	input notifications.ListAdminEmailsInput,
) (notifications.ListAdminEmailsResult, error) {
	f.listInput = input
	return f.listResult, f.listErr
}

func (f *fakeAdminEmailOutboxService) RetryFailedEmail(
	_ context.Context,
	emailID int64,
) (notifications.AdminEmail, error) {
	f.retryID = emailID
	return f.retryEmail, f.retryErr
}

func TestAdminEmailOutboxHandlerList(t *testing.T) {
	now := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	service := &fakeAdminEmailOutboxService{
		listResult: notifications.ListAdminEmailsResult{
			Emails: []notifications.AdminEmail{{
				ID: 9, BookingID: 4, EventType: notifications.BookingConfirmedEvent,
				RecipientEmail: "guest@example.com", Status: "failed", Attempts: 5,
				AvailableAt: pgtype.Timestamptz{Time: now, Valid: true},
				LastError:   pgtype.Text{String: "SMTP unavailable", Valid: true},
				CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
				UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
			}},
			Pagination: notifications.AdminEmailPagination{Page: 2, PageSize: 10, HasMore: true},
		},
	}
	handler := NewAdminEmailOutboxHandler(service)
	request := httptest.NewRequest(http.MethodGet, "/admin/email-outbox?status=FAILED&page=2&page_size=10", nil)
	response := httptest.NewRecorder()

	handler.List(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", response.Code, response.Body.String())
	}
	if service.listInput.Status == nil || *service.listInput.Status != "failed" ||
		service.listInput.Page != 2 || service.listInput.PageSize != 10 {
		t.Errorf("service input = %+v", service.listInput)
	}
	var body ListAdminEmailOutboxResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Emails) != 1 || body.Emails[0].ID != 9 ||
		body.Emails[0].LastError == nil || *body.Emails[0].LastError != "SMTP unavailable" {
		t.Errorf("response = %+v", body)
	}
}

func TestAdminEmailOutboxHandlerListRejectsInvalidPagination(t *testing.T) {
	for _, path := range []string{
		"/admin/email-outbox?page=0",
		"/admin/email-outbox?page_size=101",
		"/admin/email-outbox?page=invalid",
	} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewAdminEmailOutboxHandler(&fakeAdminEmailOutboxService{}).List(
				response,
				httptest.NewRequest(http.MethodGet, path, nil),
			)
			if response.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", response.Code)
			}
		})
	}
}

func TestAdminEmailOutboxHandlerRetry(t *testing.T) {
	now := time.Now().UTC()
	service := &fakeAdminEmailOutboxService{retryEmail: notifications.AdminEmail{
		ID: 12, Status: "pending",
		AvailableAt: pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	}}
	handler := NewAdminEmailOutboxHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/admin/email-outbox/12/retry", nil)
	request.SetPathValue("emailID", "12")
	response := httptest.NewRecorder()

	handler.Retry(response, request)

	if response.Code != http.StatusOK || service.retryID != 12 {
		t.Fatalf("status = %d, retry ID = %d; body = %q", response.Code, service.retryID, response.Body.String())
	}
}

func TestAdminEmailOutboxHandlerRetryMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		pathID     string
		err        error
		wantStatus int
	}{
		{name: "invalid ID", pathID: "bad", wantStatus: http.StatusBadRequest},
		{name: "missing", pathID: "1", err: notifications.ErrAdminEmailNotFound, wantStatus: http.StatusNotFound},
		{name: "wrong state", pathID: "1", err: notifications.ErrAdminEmailNotFailed, wantStatus: http.StatusConflict},
		{name: "internal", pathID: "1", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeAdminEmailOutboxService{retryErr: test.err}
			request := httptest.NewRequest(http.MethodPost, "/admin/email-outbox/"+test.pathID+"/retry", nil)
			request.SetPathValue("emailID", test.pathID)
			response := httptest.NewRecorder()
			NewAdminEmailOutboxHandler(service).Retry(response, request)
			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d; body = %q", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

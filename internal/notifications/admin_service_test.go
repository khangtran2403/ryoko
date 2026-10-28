package notification

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type fakeAdminEmailQueries struct {
	listParams sqlc.ListEmailOutboxForAdminParams
	listRows   []sqlc.ListEmailOutboxForAdminRow
	listErr    error
	retryID    int64
	retryCalls int
	retryRow   sqlc.RetryFailedEmailOutboxRow
	retryErr   error
	getRow     sqlc.EmailOutbox
	getErr     error
}

func (f *fakeAdminEmailQueries) ListEmailOutboxForAdmin(
	_ context.Context,
	params sqlc.ListEmailOutboxForAdminParams,
) ([]sqlc.ListEmailOutboxForAdminRow, error) {
	f.listParams = params
	return f.listRows, f.listErr
}

func (f *fakeAdminEmailQueries) RetryFailedEmailOutbox(
	_ context.Context,
	emailID int64,
) (sqlc.RetryFailedEmailOutboxRow, error) {
	f.retryID = emailID
	f.retryCalls++
	return f.retryRow, f.retryErr
}

func (f *fakeAdminEmailQueries) GetEmailOutboxByID(
	_ context.Context,
	_ int64,
) (sqlc.EmailOutbox, error) {
	return f.getRow, f.getErr
}

func TestAdminServiceListEmailsFiltersAndPaginates(t *testing.T) {
	queries := &fakeAdminEmailQueries{listRows: []sqlc.ListEmailOutboxForAdminRow{
		{ID: 3, Status: "failed"},
		{ID: 2, Status: "failed"},
		{ID: 1, Status: "failed"},
	}}
	service := NewAdminService(queries)
	status := "failed"

	result, err := service.ListEmails(context.Background(), ListAdminEmailsInput{
		Status: &status, Page: 2, PageSize: 2,
	})
	if err != nil {
		t.Fatalf("ListEmails() error = %v", err)
	}
	if len(result.Emails) != 2 || result.Emails[0].ID != 3 || result.Emails[1].ID != 2 {
		t.Errorf("emails = %+v, want IDs 3 and 2", result.Emails)
	}
	if !result.Pagination.HasMore || result.Pagination.Page != 2 || result.Pagination.PageSize != 2 {
		t.Errorf("pagination = %+v", result.Pagination)
	}
	if !queries.listParams.Status.Valid || queries.listParams.Status.String != "failed" {
		t.Errorf("status params = %+v", queries.listParams.Status)
	}
	if queries.listParams.ResultLimit != 3 || queries.listParams.ResultOffset != 2 {
		t.Errorf("query pagination = %+v, want limit 3 offset 2", queries.listParams)
	}
}

func TestAdminServiceListEmailsReturnsNonNilEmptySlice(t *testing.T) {
	service := NewAdminService(&fakeAdminEmailQueries{})
	result, err := service.ListEmails(context.Background(), ListAdminEmailsInput{
		Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("ListEmails() error = %v", err)
	}
	if result.Emails == nil || len(result.Emails) != 0 {
		t.Errorf("emails = %#v, want non-nil empty slice", result.Emails)
	}
}

func TestAdminServiceListEmailsValidatesInput(t *testing.T) {
	valid := ListAdminEmailsInput{Page: 1, PageSize: 20}
	tests := []struct {
		name    string
		mutate  func(*ListAdminEmailsInput)
		wantErr error
	}{
		{name: "invalid status", mutate: func(input *ListAdminEmailsInput) {
			status := "unknown"
			input.Status = &status
		}, wantErr: ErrInvalidAdminEmailStatus},
		{name: "invalid page", mutate: func(input *ListAdminEmailsInput) { input.Page = 0 }, wantErr: ErrInvalidAdminEmailPage},
		{name: "invalid page size", mutate: func(input *ListAdminEmailsInput) { input.PageSize = 101 }, wantErr: ErrInvalidAdminEmailPageSize},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			service := NewAdminService(&fakeAdminEmailQueries{})
			_, err := service.ListEmails(context.Background(), input)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestAdminServiceRetryFailedEmail(t *testing.T) {
	queries := &fakeAdminEmailQueries{retryRow: sqlc.RetryFailedEmailOutboxRow{
		ID: 7, Status: "pending", Attempts: 0,
		LastError: pgtype.Text{},
	}}
	service := NewAdminService(queries)

	email, err := service.RetryFailedEmail(context.Background(), 7)
	if err != nil {
		t.Fatalf("RetryFailedEmail() error = %v", err)
	}
	if queries.retryID != 7 || email.ID != 7 || email.Status != "pending" || email.Attempts != 0 {
		t.Errorf("retry result = %+v, query ID = %d", email, queries.retryID)
	}
}

func TestAdminServiceRetryFailedEmailMapsMissingAndWrongState(t *testing.T) {
	tests := []struct {
		name    string
		queries *fakeAdminEmailQueries
		wantErr error
	}{
		{
			name: "missing",
			queries: &fakeAdminEmailQueries{
				retryErr: pgx.ErrNoRows, getErr: pgx.ErrNoRows,
			},
			wantErr: ErrAdminEmailNotFound,
		},
		{
			name: "not failed",
			queries: &fakeAdminEmailQueries{
				retryErr: pgx.ErrNoRows, getRow: sqlc.EmailOutbox{ID: 8, Status: "sent"},
			},
			wantErr: ErrAdminEmailNotFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewAdminService(test.queries).RetryFailedEmail(context.Background(), 8)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestAdminServiceRetryFailedEmailRejectsInvalidIDWithoutQuery(t *testing.T) {
	queries := &fakeAdminEmailQueries{}
	_, err := NewAdminService(queries).RetryFailedEmail(context.Background(), 0)
	if !errors.Is(err, ErrAdminEmailNotFound) {
		t.Fatalf("error = %v, want ErrAdminEmailNotFound", err)
	}
	if queries.retryCalls != 0 {
		t.Errorf("retry query calls = %d, want 0", queries.retryCalls)
	}
}

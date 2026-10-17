package payments

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type fakePaymentQueries struct {
	mu sync.Mutex

	getInitiatableFn func(context.Context, sqlc.GetInitiatablePaymentForUserParams) (sqlc.Payment, error)
	getForUserFn     func(context.Context, sqlc.GetPaymentByBookingIDForUserParams) (sqlc.Payment, error)
	setProviderIDFn  func(context.Context, sqlc.SetPaymentProviderIDParams) (sqlc.Payment, error)
	getByBookingIDFn func(context.Context, int64) (sqlc.Payment, error)

	getInitiatableCalls []sqlc.GetInitiatablePaymentForUserParams
	getForUserCalls     []sqlc.GetPaymentByBookingIDForUserParams
	setProviderIDCalls  []sqlc.SetPaymentProviderIDParams
	getByBookingIDCalls []int64
}

func (q *fakePaymentQueries) GetPaymentByBookingIDForUser(
	ctx context.Context,
	arg sqlc.GetPaymentByBookingIDForUserParams,
) (sqlc.Payment, error) {
	q.mu.Lock()
	q.getForUserCalls = append(q.getForUserCalls, arg)
	fn := q.getForUserFn
	q.mu.Unlock()
	if fn == nil {
		panic("unexpected GetPaymentByBookingIDForUser call")
	}
	return fn(ctx, arg)
}

var _ paymentQueries = (*fakePaymentQueries)(nil)

func (q *fakePaymentQueries) GetInitiatablePaymentForUser(
	ctx context.Context,
	arg sqlc.GetInitiatablePaymentForUserParams,
) (sqlc.Payment, error) {
	q.mu.Lock()
	q.getInitiatableCalls = append(q.getInitiatableCalls, arg)
	fn := q.getInitiatableFn
	q.mu.Unlock()

	if fn == nil {
		panic("unexpected GetInitiatablePaymentForUser call")
	}
	return fn(ctx, arg)
}

func (q *fakePaymentQueries) SetPaymentProviderID(
	ctx context.Context,
	arg sqlc.SetPaymentProviderIDParams,
) (sqlc.Payment, error) {
	q.mu.Lock()
	q.setProviderIDCalls = append(q.setProviderIDCalls, arg)
	fn := q.setProviderIDFn
	q.mu.Unlock()

	if fn == nil {
		panic("unexpected SetPaymentProviderID call")
	}
	return fn(ctx, arg)
}

func (q *fakePaymentQueries) GetPaymentByBookingID(
	ctx context.Context,
	bookingID int64,
) (sqlc.Payment, error) {
	q.mu.Lock()
	q.getByBookingIDCalls = append(q.getByBookingIDCalls, bookingID)
	fn := q.getByBookingIDFn
	q.mu.Unlock()

	if fn == nil {
		panic("unexpected GetPaymentByBookingID call")
	}
	return fn(ctx, bookingID)
}

func (q *fakePaymentQueries) callCounts() (getInitiatable, setProviderID, getByBookingID int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.getInitiatableCalls), len(q.setProviderIDCalls), len(q.getByBookingIDCalls)
}

func TestNewServiceRejectsInvalidDependencies(t *testing.T) {
	t.Parallel()

	queries := &fakePaymentQueries{}

	tests := []struct {
		name      string
		queries   paymentQueries
		gateway   Gateway
		wantPanic string
	}{
		{
			name:      "nil queries",
			gateway:   &recordingGateway{provider: "fake"},
			wantPanic: "payment queries must not be nil",
		},
		{
			name:      "nil gateway",
			queries:   queries,
			wantPanic: "payment gateway must not be nil",
		},
		{
			name:      "blank provider",
			queries:   queries,
			gateway:   &recordingGateway{provider: "   "},
			wantPanic: "payment gateway provider must not be empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				value := recover()
				if value == nil {
					t.Fatal("NewService() did not panic")
				}
				if got := value.(string); got != test.wantPanic {
					t.Fatalf("NewService() panic = %q, want %q", got, test.wantPanic)
				}
			}()

			NewService(test.queries, test.gateway)
		})
	}
}

func TestGetPaymentStatusReturnsOwnedPayment(t *testing.T) {
	want := testPayment()
	queries := &fakePaymentQueries{
		getForUserFn: func(_ context.Context, arg sqlc.GetPaymentByBookingIDForUserParams) (sqlc.Payment, error) {
			if arg.UserID != 42 || arg.BookingID != want.BookingID {
				t.Errorf("query args = %+v, want user 42 booking %d", arg, want.BookingID)
			}
			return want, nil
		},
	}
	service := NewService(queries, &recordingGateway{provider: FakeProvider})
	got, err := service.GetPaymentStatus(context.Background(), 42, want.BookingID)
	if err != nil {
		t.Fatalf("GetPaymentStatus() error = %v", err)
	}
	if got != want {
		t.Errorf("GetPaymentStatus() = %+v, want %+v", got, want)
	}
}

func TestGetPaymentStatusValidatesAndHidesOwnership(t *testing.T) {
	tests := []struct {
		name      string
		userID    int64
		bookingID int64
		queryErr  error
		wantErr   error
		wantQuery bool
	}{
		{name: "invalid booking", userID: 42, bookingID: 0, wantErr: ErrInvalidBookingID},
		{name: "invalid user", userID: 0, bookingID: 29, wantErr: ErrInvalidUserID},
		{name: "missing or not owned", userID: 42, bookingID: 29, queryErr: pgx.ErrNoRows, wantErr: ErrPaymentNotFound, wantQuery: true},
		{name: "database failure", userID: 42, bookingID: 29, queryErr: errors.New("database failed"), wantQuery: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries := &fakePaymentQueries{
				getForUserFn: func(context.Context, sqlc.GetPaymentByBookingIDForUserParams) (sqlc.Payment, error) {
					return sqlc.Payment{}, tt.queryErr
				},
			}
			service := NewService(queries, &recordingGateway{provider: FakeProvider})
			_, err := service.GetPaymentStatus(context.Background(), tt.userID, tt.bookingID)
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("GetPaymentStatus() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && err == nil {
				t.Fatal("GetPaymentStatus() error = nil, want database error")
			}
			if got := len(queries.getForUserCalls); (got == 1) != tt.wantQuery {
				t.Errorf("query calls = %d, wantQuery=%t", got, tt.wantQuery)
			}
		})
	}
}

func TestInitiatePaymentValidatesIDsBeforeDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userID    int64
		bookingID int64
		wantErr   error
	}{
		{name: "invalid booking", userID: 7, bookingID: 0, wantErr: ErrInvalidBookingID},
		{name: "invalid user", userID: 0, bookingID: 11, wantErr: ErrInvalidUserID},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			queries := &fakePaymentQueries{}
			gateway := &recordingGateway{provider: "fake"}
			service := NewService(queries, gateway)

			_, err := service.InitiatePayment(context.Background(), test.userID, test.bookingID)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("InitiatePayment() error = %v, want %v", err, test.wantErr)
			}

			getCalls, setCalls, reloadCalls := queries.callCounts()
			if getCalls != 0 || setCalls != 0 || reloadCalls != 0 {
				t.Fatalf("query call counts = (%d, %d, %d), want all zero", getCalls, setCalls, reloadCalls)
			}
			if len(gateway.recordedCreateCalls()) != 0 {
				t.Fatal("gateway was called for invalid input")
			}
		})
	}
}

func TestInitiatePaymentMapsUnavailableAndQueryErrors(t *testing.T) {
	t.Parallel()

	databaseErr := errors.New("database unavailable")
	tests := []struct {
		name     string
		queryErr error
		wantErr  error
	}{
		{name: "not available", queryErr: pgx.ErrNoRows, wantErr: ErrPaymentNotAvailable},
		{name: "database failure", queryErr: databaseErr, wantErr: databaseErr},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			queries := &fakePaymentQueries{
				getInitiatableFn: func(
					context.Context,
					sqlc.GetInitiatablePaymentForUserParams,
				) (sqlc.Payment, error) {
					return sqlc.Payment{}, test.queryErr
				},
			}
			gateway := &recordingGateway{provider: "fake"}
			service := NewService(queries, gateway)

			_, err := service.InitiatePayment(context.Background(), 7, 11)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("InitiatePayment() error = %v, want %v", err, test.wantErr)
			}
			if len(gateway.recordedCreateCalls()) != 0 {
				t.Fatal("gateway was called after payment lookup failed")
			}
		})
	}
}

func TestInitiatePaymentRejectsProviderMismatch(t *testing.T) {
	t.Parallel()

	queries := queriesReturningPayment(testPayment())
	gateway := &recordingGateway{provider: "other-provider"}
	service := NewService(queries, gateway)

	_, err := service.InitiatePayment(context.Background(), 7, 11)
	if !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("InitiatePayment() error = %v, want ErrProviderMismatch", err)
	}
	if len(gateway.recordedCreateCalls()) != 0 {
		t.Fatal("gateway was called despite provider mismatch")
	}
}

func TestInitiatePaymentCreatesAndStoresProviderPayment(t *testing.T) {
	t.Parallel()

	payment := testPayment()
	storedPayment := payment
	storedPayment.ProviderPaymentID = pgtype.Text{String: "provider-payment-17", Valid: true}

	queries := queriesReturningPayment(payment)
	queries.setProviderIDFn = func(
		_ context.Context,
		arg sqlc.SetPaymentProviderIDParams,
	) (sqlc.Payment, error) {
		if arg.PaymentID != payment.ID {
			t.Errorf("SetPaymentProviderID() payment ID = %d, want %d", arg.PaymentID, payment.ID)
		}
		if !arg.ProviderPaymentID.Valid || arg.ProviderPaymentID.String != "provider-payment-17" {
			t.Errorf("SetPaymentProviderID() provider ID = %+v", arg.ProviderPaymentID)
		}
		return storedPayment, nil
	}

	gateway := &recordingGateway{
		provider: "fake",
		createSession: PaymentSession{
			ProviderPaymentID: "  provider-payment-17  ",
			CheckoutURL:       "  https://payments.example/checkout/17  ",
		},
	}
	service := NewService(queries, gateway)

	result, err := service.InitiatePayment(context.Background(), 7, 11)
	if err != nil {
		t.Fatalf("InitiatePayment() error = %v", err)
	}
	if result.Payment != storedPayment {
		t.Errorf("InitiatePayment() payment = %+v, want %+v", result.Payment, storedPayment)
	}
	if result.Session.ProviderPaymentID != "provider-payment-17" {
		t.Errorf("provider payment ID = %q", result.Session.ProviderPaymentID)
	}
	if result.Session.CheckoutURL != "https://payments.example/checkout/17" {
		t.Errorf("checkout URL = %q", result.Session.CheckoutURL)
	}

	calls := gateway.recordedCreateCalls()
	if len(calls) != 1 {
		t.Fatalf("CreatePayment() call count = %d, want 1", len(calls))
	}
	wantRequest := CreatePaymentRequest{
		PaymentID:      payment.ID,
		BookingID:      payment.BookingID,
		AmountMinor:    payment.AmountMinor,
		Currency:       payment.Currency,
		IdempotencyKey: payment.ProviderIdempotencyKey,
	}
	if calls[0] != wantRequest {
		t.Errorf("CreatePayment() request = %+v, want %+v", calls[0], wantRequest)
	}

	getCalls, setCalls, reloadCalls := queries.callCounts()
	if getCalls != 1 || setCalls != 1 || reloadCalls != 0 {
		t.Fatalf("query call counts = (%d, %d, %d), want (1, 1, 0)", getCalls, setCalls, reloadCalls)
	}
}

func TestInitiatePaymentPropagatesGatewayError(t *testing.T) {
	t.Parallel()

	queries := queriesReturningPayment(testPayment())
	gateway := &recordingGateway{
		provider:  "fake",
		createErr: ErrGatewayUnavailable,
	}
	service := NewService(queries, gateway)

	_, err := service.InitiatePayment(context.Background(), 7, 11)
	if !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("InitiatePayment() error = %v, want ErrGatewayUnavailable", err)
	}

	_, setCalls, _ := queries.callCounts()
	if setCalls != 0 {
		t.Fatal("provider payment ID was stored after gateway failure")
	}
}

func TestInitiatePaymentRejectsInvalidGatewaySessions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session PaymentSession
		wantErr error
	}{
		{
			name:    "missing provider payment ID",
			session: PaymentSession{CheckoutURL: "https://payments.example/checkout"},
			wantErr: ErrProviderPaymentIDRequired,
		},
		{
			name:    "missing action",
			session: PaymentSession{ProviderPaymentID: "provider-payment-17"},
			wantErr: ErrCheckoutURLOrClientSecretRequired,
		},
		{
			name: "two actions",
			session: PaymentSession{
				ProviderPaymentID: "provider-payment-17",
				CheckoutURL:       "https://payments.example/checkout",
				ClientSecret:      "client-secret",
			},
			wantErr: ErrCheckoutURLOrClientSecretRequired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			queries := queriesReturningPayment(testPayment())
			gateway := &recordingGateway{
				provider:      "fake",
				createSession: test.session,
			}
			service := NewService(queries, gateway)

			_, err := service.InitiatePayment(context.Background(), 7, 11)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("InitiatePayment() error = %v, want %v", err, test.wantErr)
			}

			_, setCalls, _ := queries.callCounts()
			if setCalls != 0 {
				t.Fatal("provider payment ID was stored for an invalid gateway session")
			}
		})
	}
}

func TestInitiatePaymentReusesMatchingProviderPayment(t *testing.T) {
	t.Parallel()

	payment := testPayment()
	payment.ProviderPaymentID = pgtype.Text{String: "provider-payment-17", Valid: true}
	queries := queriesReturningPayment(payment)
	gateway := &recordingGateway{
		provider: "fake",
		createSession: PaymentSession{
			ProviderPaymentID: "provider-payment-17",
			ClientSecret:      "client-secret",
		},
	}
	service := NewService(queries, gateway)

	result, err := service.InitiatePayment(context.Background(), 7, 11)
	if err != nil {
		t.Fatalf("InitiatePayment() error = %v", err)
	}
	if result.Payment != payment {
		t.Errorf("InitiatePayment() payment = %+v, want %+v", result.Payment, payment)
	}
	if result.Session.ClientSecret != "client-secret" {
		t.Errorf("client secret = %q", result.Session.ClientSecret)
	}

	_, setCalls, reloadCalls := queries.callCounts()
	if setCalls != 0 || reloadCalls != 0 {
		t.Fatalf("query call counts set/reload = (%d, %d), want zero", setCalls, reloadCalls)
	}
}

func TestInitiatePaymentRejectsConflictingExistingProviderPayment(t *testing.T) {
	t.Parallel()

	payment := testPayment()
	payment.ProviderPaymentID = pgtype.Text{String: "existing-provider-payment", Valid: true}
	queries := queriesReturningPayment(payment)
	gateway := &recordingGateway{
		provider: "fake",
		createSession: PaymentSession{
			ProviderPaymentID: "different-provider-payment",
			ClientSecret:      "client-secret",
		},
	}
	service := NewService(queries, gateway)

	_, err := service.InitiatePayment(context.Background(), 7, 11)
	if !errors.Is(err, ErrProviderPaymentIDMismatch) {
		t.Fatalf("InitiatePayment() error = %v, want ErrProviderPaymentIDMismatch", err)
	}

	_, setCalls, reloadCalls := queries.callCounts()
	if setCalls != 0 || reloadCalls != 0 {
		t.Fatalf("query call counts set/reload = (%d, %d), want zero", setCalls, reloadCalls)
	}
}

func TestInitiatePaymentHandlesConcurrentProviderIDAttachment(t *testing.T) {
	t.Parallel()

	payment := testPayment()
	reloadedPayment := payment
	reloadedPayment.ProviderPaymentID = pgtype.Text{String: "provider-payment-17", Valid: true}

	queries := queriesReturningPayment(payment)
	queries.setProviderIDFn = func(
		context.Context,
		sqlc.SetPaymentProviderIDParams,
	) (sqlc.Payment, error) {
		return sqlc.Payment{}, pgx.ErrNoRows
	}
	queries.getByBookingIDFn = func(_ context.Context, bookingID int64) (sqlc.Payment, error) {
		if bookingID != payment.BookingID {
			t.Errorf("GetPaymentByBookingID() booking ID = %d, want %d", bookingID, payment.BookingID)
		}
		return reloadedPayment, nil
	}
	gateway := &recordingGateway{
		provider: "fake",
		createSession: PaymentSession{
			ProviderPaymentID: "provider-payment-17",
			CheckoutURL:       "https://payments.example/checkout/17",
		},
	}
	service := NewService(queries, gateway)

	result, err := service.InitiatePayment(context.Background(), 7, 11)
	if err != nil {
		t.Fatalf("InitiatePayment() error = %v", err)
	}
	if result.Payment != reloadedPayment {
		t.Errorf("InitiatePayment() payment = %+v, want %+v", result.Payment, reloadedPayment)
	}

	_, setCalls, reloadCalls := queries.callCounts()
	if setCalls != 1 || reloadCalls != 1 {
		t.Fatalf("query call counts set/reload = (%d, %d), want (1, 1)", setCalls, reloadCalls)
	}
}

func TestInitiatePaymentRejectsConflictingConcurrentAttachment(t *testing.T) {
	t.Parallel()

	payment := testPayment()
	reloadedPayment := payment
	reloadedPayment.ProviderPaymentID = pgtype.Text{String: "different-provider-payment", Valid: true}

	queries := queriesReturningPayment(payment)
	queries.setProviderIDFn = func(
		context.Context,
		sqlc.SetPaymentProviderIDParams,
	) (sqlc.Payment, error) {
		return sqlc.Payment{}, pgx.ErrNoRows
	}
	queries.getByBookingIDFn = func(context.Context, int64) (sqlc.Payment, error) {
		return reloadedPayment, nil
	}
	gateway := &recordingGateway{
		provider: "fake",
		createSession: PaymentSession{
			ProviderPaymentID: "provider-payment-17",
			ClientSecret:      "client-secret",
		},
	}
	service := NewService(queries, gateway)

	_, err := service.InitiatePayment(context.Background(), 7, 11)
	if !errors.Is(err, ErrProviderPaymentIDMismatch) {
		t.Fatalf("InitiatePayment() error = %v, want ErrProviderPaymentIDMismatch", err)
	}
}

func TestInitiatePaymentWrapsPersistenceErrors(t *testing.T) {
	t.Parallel()

	payment := testPayment()
	databaseErr := errors.New("database unavailable")
	gateway := &recordingGateway{
		provider: "fake",
		createSession: PaymentSession{
			ProviderPaymentID: "provider-payment-17",
			ClientSecret:      "client-secret",
		},
	}

	t.Run("set provider ID", func(t *testing.T) {
		queries := queriesReturningPayment(payment)
		queries.setProviderIDFn = func(
			context.Context,
			sqlc.SetPaymentProviderIDParams,
		) (sqlc.Payment, error) {
			return sqlc.Payment{}, databaseErr
		}

		_, err := NewService(queries, gateway).InitiatePayment(context.Background(), 7, 11)
		if !errors.Is(err, databaseErr) || !strings.Contains(err.Error(), "set payment provider ID") {
			t.Fatalf("InitiatePayment() error = %v", err)
		}
	})

	t.Run("reload after concurrent update", func(t *testing.T) {
		queries := queriesReturningPayment(payment)
		queries.setProviderIDFn = func(
			context.Context,
			sqlc.SetPaymentProviderIDParams,
		) (sqlc.Payment, error) {
			return sqlc.Payment{}, pgx.ErrNoRows
		}
		queries.getByBookingIDFn = func(context.Context, int64) (sqlc.Payment, error) {
			return sqlc.Payment{}, databaseErr
		}

		_, err := NewService(queries, gateway).InitiatePayment(context.Background(), 7, 11)
		if !errors.Is(err, databaseErr) || !strings.Contains(err.Error(), "reload payment") {
			t.Fatalf("InitiatePayment() error = %v", err)
		}
	})
}

func queriesReturningPayment(payment sqlc.Payment) *fakePaymentQueries {
	return &fakePaymentQueries{
		getInitiatableFn: func(
			context.Context,
			sqlc.GetInitiatablePaymentForUserParams,
		) (sqlc.Payment, error) {
			return payment, nil
		},
	}
}

func testPayment() sqlc.Payment {
	return sqlc.Payment{
		ID:                     17,
		BookingID:              11,
		Provider:               "fake",
		ProviderIdempotencyKey: "payment-17-create",
		AmountMinor:            250_000,
		Currency:               "VND",
		Status:                 "pending",
	}
}

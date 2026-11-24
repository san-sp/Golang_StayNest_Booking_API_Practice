package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"staynest/internal/payment/razorpay"
)

type mockRefundProvider struct {
	mu sync.Mutex

	createResult *razorpay.Refund
	fetchResult  *razorpay.Refund

	createErr error
	fetchErr  error

	createCalls int
	fetchCalls  int

	createInputs []razorpay.CreateRefundInput
	fetchIDs     []string
	afterCreate  func()
}

func (m *mockRefundProvider) CreateRefund(
	ctx context.Context,
	input *razorpay.CreateRefundInput,
) (*razorpay.Refund, error) {
	m.mu.Lock()

	m.createCalls++

	if input != nil {
		m.createInputs = append(m.createInputs, *input)
	}

	if m.createErr != nil {
		err := m.createErr
		m.mu.Unlock()
		return nil, err
	}

	if m.createResult == nil {
		m.mu.Unlock()
		return nil, errors.New("unexpected CreateRefund call")
	}

	result := *m.createResult
	callback := m.afterCreate

	m.mu.Unlock()

	// Simulate an interruption after the provider responds
	// but before the service persists the provider refund ID.
	if callback != nil {
		callback()
	}

	return &result, nil
}

func (m *mockRefundProvider) FetchRefund(
	ctx context.Context,
	refundID string,
) (*razorpay.Refund, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fetchCalls++
	m.fetchIDs = append(m.fetchIDs, refundID)

	if m.fetchErr != nil {
		return nil, m.fetchErr
	}

	if m.fetchResult == nil {
		return nil, errors.New("unexpected FetchRefund call")
	}

	result := *m.fetchResult
	return &result, nil
}

func (m *mockRefundProvider) counts() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createCalls, m.fetchCalls
}

func newTestRefundService(
	t *testing.T,
	repo *Repository,
	provider *mockRefundProvider,
) *RefundService {
	t.Helper()

	service, err := NewRefundService(
		repo,
		provider,
		10,
		defaultRefundLeaseDuration,
		defaultRefundRetryDelay,
	)
	if err != nil {
		t.Fatalf("NewRefundService() error = %v", err)
	}

	return service
}

func TestRefundServiceProcessRefundSuccess(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	provider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        "rfnd_service_test_001",
			PaymentID: refund.ProviderPaymentID,
			Amount:    refund.AmountPaise,
			Currency:  refund.Currency,
			Status:    "processed",
		},
	}

	service := newTestRefundService(t, repo, provider)

	err := service.processRefund(
		context.Background(),
		refund,
	)
	if err != nil {
		t.Fatalf("processRefund() error = %v", err)
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 1 {
		t.Errorf("expected 1 CreateRefund call, got %d", createCalls)
	}

	if fetchCalls != 0 {
		t.Errorf("expected 0 FetchRefund calls, got %d", fetchCalls)
	}

	var (
		status           string
		providerRefundID *string
		storedLeaseToken *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			processing_lease_token
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&providerRefundID,
		&storedLeaseToken,
	)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusSucceeded {
		t.Errorf(
			"expected status %q, got %q",
			RefundStatusSucceeded,
			status,
		)
	}

	if providerRefundID == nil ||
		*providerRefundID != "rfnd_service_test_001" {
		t.Errorf("unexpected provider refund ID: %v", providerRefundID)
	}

	if storedLeaseToken != nil {
		t.Errorf("expected processing lease to be cleared, got %v", storedLeaseToken)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()

	if len(provider.createInputs) != 1 {
		t.Fatalf(
			"expected 1 recorded create input, got %d",
			len(provider.createInputs),
		)
	}

	input := provider.createInputs[0]

	if input.PaymentID != refund.ProviderPaymentID {
		t.Errorf("unexpected provider payment ID: %q", input.PaymentID)
	}

	if input.AmountPaise != refund.AmountPaise {
		t.Errorf("unexpected refund amount: %d", input.AmountPaise)
	}

	if input.IdempotencyKey != refund.IdempotencyKey {
		t.Errorf("unexpected idempotency key: %q", input.IdempotencyKey)
	}

	_ = leaseToken
}

func TestRefundServiceProcessRefundRecovery(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, leaseToken := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	providerRefundID := "rfnd_recovery_001"

	// Simulate the previous worker successfully creating a refund
	// and persisting its Razorpay refund ID before crashing.
	err := repo.RecordProviderRefundID(
		context.Background(),
		refund.ID,
		leaseToken,
		providerRefundID,
	)
	if err != nil {
		t.Fatalf("RecordProviderRefundID() error = %v", err)
	}

	refund.ProviderRefundID = &providerRefundID

	provider := &mockRefundProvider{
		fetchResult: &razorpay.Refund{
			ID:        providerRefundID,
			PaymentID: refund.ProviderPaymentID,
			Amount:    refund.AmountPaise,
			Currency:  refund.Currency,
			Status:    "processed",
		},
	}

	service := newTestRefundService(t, repo, provider)

	err = service.processRefund(
		context.Background(),
		refund,
	)
	if err != nil {
		t.Fatalf("processRefund() error = %v", err)
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 0 {
		t.Errorf(
			"expected 0 CreateRefund calls, got %d",
			createCalls,
		)
	}

	if fetchCalls != 1 {
		t.Errorf(
			"expected 1 FetchRefund call, got %d",
			fetchCalls,
		)
	}

	provider.mu.Lock()

	if len(provider.fetchIDs) != 1 {
		provider.mu.Unlock()
		t.Fatalf(
			"expected 1 recorded fetch ID, got %d",
			len(provider.fetchIDs),
		)
	}

	fetchedID := provider.fetchIDs[0]

	provider.mu.Unlock()

	if fetchedID != providerRefundID {
		t.Errorf(
			"expected fetched refund ID %q, got %q",
			providerRefundID,
			fetchedID,
		)
	}

	var (
		status           string
		storedRefundID   *string
		storedLeaseToken *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			processing_lease_token
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&storedRefundID,
		&storedLeaseToken,
	)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusSucceeded {
		t.Errorf(
			"expected status %q, got %q",
			RefundStatusSucceeded,
			status,
		)
	}

	if storedRefundID == nil ||
		*storedRefundID != providerRefundID {
		t.Errorf(
			"unexpected stored provider refund ID: %v",
			storedRefundID,
		)
	}

	if storedLeaseToken != nil {
		t.Errorf(
			"expected processing lease to be cleared, got %v",
			storedLeaseToken,
		)
	}
}

func TestRefundServiceProcessRefundPending(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, _ := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	providerRefundID := "rfnd_pending_001"

	provider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        providerRefundID,
			PaymentID: refund.ProviderPaymentID,
			Amount:    refund.AmountPaise,
			Currency:  refund.Currency,
			Status:    "pending",
		},
	}

	service := newTestRefundService(t, repo, provider)

	err := service.processRefund(context.Background(), refund)
	if err != nil {
		t.Fatalf("processRefund() error = %v", err)
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 1 {
		t.Errorf("expected 1 CreateRefund call, got %d", createCalls)
	}

	if fetchCalls != 0 {
		t.Errorf("expected 0 FetchRefund calls, got %d", fetchCalls)
	}

	var (
		status         string
		storedRefundID *string
		leaseToken     *string
		nextAttemptAt  time.Time
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			processing_lease_token,
			next_attempt_at
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&storedRefundID,
		&leaseToken,
		&nextAttemptAt,
	)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusPending {
		t.Errorf("expected status %q, got %q", RefundStatusPending, status)
	}

	if storedRefundID == nil || *storedRefundID != providerRefundID {
		t.Errorf("unexpected provider refund ID: %v", storedRefundID)
	}

	if leaseToken != nil {
		t.Errorf("expected processing lease to be cleared, got %v", leaseToken)
	}

	if !nextAttemptAt.After(time.Now().UTC()) {
		t.Errorf("expected next attempt to be in the future, got %v", nextAttemptAt)
	}
}

func TestRefundServiceProcessRefundFailed(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, _ := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	providerRefundID := "rfnd_failed_001"

	provider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        providerRefundID,
			PaymentID: refund.ProviderPaymentID,
			Amount:    refund.AmountPaise,
			Currency:  refund.Currency,
			Status:    "failed",
		},
	}

	service := newTestRefundService(t, repo, provider)

	err := service.processRefund(context.Background(), refund)
	if err != nil {
		t.Fatalf("processRefund() error = %v", err)
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 1 {
		t.Errorf("expected 1 CreateRefund call, got %d", createCalls)
	}

	if fetchCalls != 0 {
		t.Errorf("expected 0 FetchRefund calls, got %d", fetchCalls)
	}

	var (
		status         string
		storedRefundID *string
		leaseToken     *string
		lastError      *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			processing_lease_token,
			last_error
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&storedRefundID,
		&leaseToken,
		&lastError,
	)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusFailed {
		t.Errorf("expected status %q, got %q", RefundStatusFailed, status)
	}

	if storedRefundID == nil || *storedRefundID != providerRefundID {
		t.Errorf("unexpected provider refund ID: %v", storedRefundID)
	}

	if leaseToken != nil {
		t.Errorf("expected processing lease to be cleared, got %v", leaseToken)
	}

	if lastError == nil || *lastError == "" {
		t.Error("expected failure reason to be stored")
	}
}

func TestRefundServiceProcessRefundRetry(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, _ := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	providerErr := errors.New("simulated network timeout")

	provider := &mockRefundProvider{
		createErr: providerErr,
	}

	service := newTestRefundService(t, repo, provider)

	err := service.processRefund(context.Background(), refund)

	if !errors.Is(err, providerErr) {
		t.Fatalf("expected provider error %v, got %v", providerErr, err)
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 1 {
		t.Errorf("expected 1 CreateRefund call, got %d", createCalls)
	}

	if fetchCalls != 0 {
		t.Errorf("expected 0 FetchRefund calls, got %d", fetchCalls)
	}

	var (
		status        string
		leaseToken    *string
		lastError     *string
		nextAttemptAt time.Time
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			processing_lease_token,
			last_error,
			next_attempt_at
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&leaseToken,
		&lastError,
		&nextAttemptAt,
	)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusPending {
		t.Errorf("expected status %q, got %q", RefundStatusPending, status)
	}

	if leaseToken != nil {
		t.Errorf("expected processing lease to be cleared, got %v", leaseToken)
	}

	if lastError == nil || *lastError != providerErr.Error() {
		t.Errorf("unexpected last error: %v", lastError)
	}

	if !nextAttemptAt.After(time.Now().UTC()) {
		t.Errorf("expected next attempt in the future, got %v", nextAttemptAt)
	}
}

func TestRefundServiceRetryUsesSameIdempotencyKey(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, _ := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	providerErr := errors.New("simulated network timeout")

	provider := &mockRefundProvider{
		createErr: providerErr,
	}

	service := newTestRefundService(t, repo, provider)

	// First attempt: provider call times out.
	err := service.processRefund(context.Background(), refund)
	if !errors.Is(err, providerErr) {
		t.Fatalf("expected provider error, got %v", err)
	}

	// Make the scheduled retry immediately eligible.
	_, err = pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET next_attempt_at = NOW()
		WHERE id = $1
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to make refund retry eligible: %v", err)
	}

	// Second attempt: the worker claims the refund again.
	err = service.ProcessPendingBatch(context.Background())
	if !errors.Is(err, providerErr) {
		t.Fatalf("expected provider error on retry, got %v", err)
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 2 {
		t.Fatalf("expected 2 CreateRefund calls, got %d", createCalls)
	}

	if fetchCalls != 0 {
		t.Errorf("expected 0 FetchRefund calls, got %d", fetchCalls)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()

	if len(provider.createInputs) != 2 {
		t.Fatalf(
			"expected 2 create inputs, got %d",
			len(provider.createInputs),
		)
	}

	firstKey := provider.createInputs[0].IdempotencyKey
	secondKey := provider.createInputs[1].IdempotencyKey

	if firstKey == "" {
		t.Fatal("expected a non-empty idempotency key")
	}

	if firstKey != secondKey {
		t.Errorf(
			"idempotency key changed between attempts: first=%q second=%q",
			firstKey,
			secondKey,
		)
	}
}

func TestRefundServiceInvalidProviderPaymentID(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, _ := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	provider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        "rfnd_wrong_payment_001",
			PaymentID: "pay_wrong_payment",
			Amount:    refund.AmountPaise,
			Currency:  refund.Currency,
			Status:    "processed",
		},
	}

	service := newTestRefundService(t, repo, provider)

	err := service.processRefund(context.Background(), refund)
	if !errors.Is(err, ErrRefundLeaseLost) {
		// The service records the review-required state and returns
		// the repository result. The original validation error is
		// currently not returned by markRefundForReview.
		if err != nil {
			t.Fatalf("processRefund() error = %v", err)
		}
	}

	var (
		status     string
		lastError  *string
		leaseToken *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			last_error,
			processing_lease_token
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&lastError,
		&leaseToken,
	)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusReviewRequired {
		t.Errorf(
			"expected status %q, got %q",
			RefundStatusReviewRequired,
			status,
		)
	}

	if lastError == nil || *lastError == "" {
		t.Error("expected review reason to be stored")
	}

	if leaseToken != nil {
		t.Errorf("expected processing lease to be cleared, got %v", leaseToken)
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 1 || fetchCalls != 0 {
		t.Errorf(
			"expected 1 create and 0 fetch calls, got %d create and %d fetch",
			createCalls,
			fetchCalls,
		)
	}
}

func TestRefundServiceUnknownProviderStatus(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, _ := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	provider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        "rfnd_unknown_status_001",
			PaymentID: refund.ProviderPaymentID,
			Amount:    refund.AmountPaise,
			Currency:  refund.Currency,
			Status:    "queued_for_manual_review",
		},
	}

	service := newTestRefundService(t, repo, provider)

	err := service.processRefund(context.Background(), refund)
	if err != nil {
		t.Fatalf("processRefund() error = %v", err)
	}

	var (
		status     string
		lastError  *string
		leaseToken *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT status, last_error, processing_lease_token
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(&status, &lastError, &leaseToken)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusReviewRequired {
		t.Errorf("expected %q, got %q", RefundStatusReviewRequired, status)
	}

	if lastError == nil ||
		!strings.Contains(*lastError, "queued_for_manual_review") {
		t.Errorf("unexpected review reason: %v", lastError)
	}

	if leaseToken != nil {
		t.Errorf("expected lease to be cleared, got %v", leaseToken)
	}
}

func TestRefundServiceInvalidProviderAmountOrCurrency(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
	}{
		{
			name:     "wrong refund amount",
			amount:   100,
			currency: CurrencyINR,
		},
		{
			name:     "wrong currency",
			amount:   0, // Set to the actual refund amount below.
			currency: "USD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, pool := setupPaymentRepository(t)
			fixture := createPaymentTestFixture(t, pool)

			refund, _ := createClaimedRefundForTest(
				t,
				repo,
				pool,
				fixture,
			)

			amount := tt.amount
			if amount == 0 {
				amount = refund.AmountPaise
			}

			provider := &mockRefundProvider{
				createResult: &razorpay.Refund{
					ID:        "rfnd_invalid_amount_currency",
					PaymentID: refund.ProviderPaymentID,
					Amount:    amount,
					Currency:  tt.currency,
					Status:    "processed",
				},
			}

			service := newTestRefundService(t, repo, provider)

			err := service.processRefund(context.Background(), refund)
			if err != nil {
				t.Fatalf("processRefund() error = %v", err)
			}

			var (
				status     string
				lastError  *string
				leaseToken *string
			)

			err = pool.QueryRow(context.Background(), `
				SELECT status, last_error, processing_lease_token
				FROM payment_refunds
				WHERE id = $1
			`, refund.ID).Scan(
				&status,
				&lastError,
				&leaseToken,
			)
			if err != nil {
				t.Fatalf("failed to query refund: %v", err)
			}

			if status != RefundStatusReviewRequired {
				t.Errorf(
					"expected %q, got %q",
					RefundStatusReviewRequired,
					status,
				)
			}

			if lastError == nil || *lastError == "" {
				t.Error("expected validation reason to be stored")
			}

			if leaseToken != nil {
				t.Errorf("expected lease to be cleared, got %v", leaseToken)
			}
		})
	}
}

func TestRefundServiceMissingProviderRefundID(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, _ := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	provider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        "",
			PaymentID: refund.ProviderPaymentID,
			Amount:    refund.AmountPaise,
			Currency:  refund.Currency,
			Status:    "processed",
		},
	}

	service := newTestRefundService(t, repo, provider)

	err := service.processRefund(context.Background(), refund)
	if err != nil {
		t.Fatalf("processRefund() error = %v", err)
	}

	var (
		status           string
		providerRefundID *string
		lastError        *string
		leaseToken       *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			last_error,
			processing_lease_token
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&status,
		&providerRefundID,
		&lastError,
		&leaseToken,
	)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusReviewRequired {
		t.Errorf(
			"expected %q, got %q",
			RefundStatusReviewRequired,
			status,
		)
	}

	if providerRefundID != nil {
		t.Errorf(
			"expected no provider refund ID, got %v",
			*providerRefundID,
		)
	}

	if lastError == nil || *lastError == "" {
		t.Error("expected validation reason to be stored")
	}

	if leaseToken != nil {
		t.Errorf("expected lease to be cleared, got %v", leaseToken)
	}
}

func TestRefundServiceProviderHTTPErrorHandling(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		expectedStatus string
	}{
		{
			name:           "rate limited",
			statusCode:     429,
			expectedStatus: RefundStatusPending,
		},
		{
			name:           "internal server error",
			statusCode:     500,
			expectedStatus: RefundStatusPending,
		},
		{
			name:           "bad request",
			statusCode:     400,
			expectedStatus: RefundStatusReviewRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, pool := setupPaymentRepository(t)
			fixture := createPaymentTestFixture(t, pool)

			refund, _ := createClaimedRefundForTest(
				t,
				repo,
				pool,
				fixture,
			)

			providerErr := &razorpay.APIError{
				StatusCode:  tt.statusCode,
				Description: tt.name,
			}

			provider := &mockRefundProvider{
				createErr: providerErr,
			}

			service := newTestRefundService(t, repo, provider)

			err := service.processRefund(
				context.Background(),
				refund,
			)

			if !errors.Is(err, providerErr) {
				t.Fatalf(
					"expected provider error %v, got %v",
					providerErr,
					err,
				)
			}

			var (
				status     string
				lastError  *string
				leaseToken *string
			)

			err = pool.QueryRow(context.Background(), `
				SELECT
					status,
					last_error,
					processing_lease_token
				FROM payment_refunds
				WHERE id = $1
			`, refund.ID).Scan(
				&status,
				&lastError,
				&leaseToken,
			)
			if err != nil {
				t.Fatalf("failed to query refund: %v", err)
			}

			if status != tt.expectedStatus {
				t.Errorf(
					"expected status %q, got %q",
					tt.expectedStatus,
					status,
				)
			}

			if lastError == nil || *lastError == "" {
				t.Error("expected provider error to be stored")
			}

			if leaseToken != nil {
				t.Errorf(
					"expected processing lease to be cleared, got %v",
					leaseToken,
				)
			}
		})
	}
}

func TestRefundServiceRecoversWhenPersistingProviderRefundIDFails(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	refund, _ := createClaimedRefundForTest(
		t,
		repo,
		pool,
		fixture,
	)

	providerRefundID := "rfnd_persistence_recovery_001"

	provider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        providerRefundID,
			PaymentID: refund.ProviderPaymentID,
			Amount:    refund.AmountPaise,
			Currency:  refund.Currency,
			Status:    "processed",
		},
	}

	// Cancel only the first provider call's context.
	var cancelOnce sync.Once

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	provider.afterCreate = func() {
		cancelOnce.Do(cancel)
	}

	service := newTestRefundService(t, repo, provider)

	// First attempt: Razorpay returns successfully,
	// but the subsequent database operation is cancelled.
	err := service.processRefund(ctx, refund)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf(
			"expected context cancellation, got %v",
			err,
		)
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 1 {
		t.Fatalf("expected 1 CreateRefund call, got %d", createCalls)
	}

	if fetchCalls != 0 {
		t.Fatalf("expected 0 FetchRefund calls, got %d", fetchCalls)
	}

	// Confirm that the provider refund ID was not persisted.
	var (
		status         string
		storedRefundID *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT status, provider_refund_id
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(&status, &storedRefundID)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusProcessing {
		t.Errorf(
			"expected status %q, got %q",
			RefundStatusProcessing,
			status,
		)
	}

	if storedRefundID != nil {
		t.Fatalf(
			"expected provider refund ID not to be persisted, got %q",
			*storedRefundID,
		)
	}

	// Simulate the processing lease expiring.
	_, err = pool.Exec(context.Background(), `
		UPDATE payment_refunds
		SET processing_lease_expires_at = NOW() - INTERVAL '1 second'
		WHERE id = $1
		  AND status = 'processing'
	`, refund.ID)
	if err != nil {
		t.Fatalf("failed to expire processing lease: %v", err)
	}

	// Second attempt: the expired-refund worker reclaims
	// the refund and retries using the original idempotency key.
	err = service.ProcessExpiredBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessExpiredBatch() error = %v", err)
	}

	createCalls, fetchCalls = provider.counts()

	if createCalls != 2 {
		t.Errorf("expected 2 CreateRefund calls, got %d", createCalls)
	}

	if fetchCalls != 0 {
		t.Errorf("expected 0 FetchRefund calls, got %d", fetchCalls)
	}

	provider.mu.Lock()

	if len(provider.createInputs) != 2 {
		provider.mu.Unlock()
		t.Fatalf(
			"expected 2 create inputs, got %d",
			len(provider.createInputs),
		)
	}

	firstKey := provider.createInputs[0].IdempotencyKey
	secondKey := provider.createInputs[1].IdempotencyKey

	provider.mu.Unlock()

	if firstKey != secondKey {
		t.Errorf(
			"idempotency key changed: first=%q second=%q",
			firstKey,
			secondKey,
		)
	}

	// Verify the final database state.
	var (
		finalStatus     string
		finalRefundID   *string
		finalLeaseToken *string
	)

	err = pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			processing_lease_token
		FROM payment_refunds
		WHERE id = $1
	`, refund.ID).Scan(
		&finalStatus,
		&finalRefundID,
		&finalLeaseToken,
	)
	if err != nil {
		t.Fatalf("failed to query final refund: %v", err)
	}

	if finalStatus != RefundStatusSucceeded {
		t.Errorf(
			"expected status %q, got %q",
			RefundStatusSucceeded,
			finalStatus,
		)
	}

	if finalRefundID == nil || *finalRefundID != providerRefundID {
		t.Errorf("unexpected final provider refund ID: %v", finalRefundID)
	}

	if finalLeaseToken != nil {
		t.Errorf("expected lease to be cleared, got %v", finalLeaseToken)
	}
}

func TestRefundServiceConcurrentWorkersDoNotDuplicateRefund(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	payment := createPaymentForLeaseTest(t, repo, fixture)

	refundID := createPendingRefundForClaimTest(
		t,
		pool,
		payment,
		time.Now().UTC().Add(-time.Minute),
	)

	providerPaymentID := fmt.Sprintf(
		"pay_test_%s",
		strings.ReplaceAll(payment.ID, "-", ""),
	)

	providerRefundID := "rfnd_concurrent_service_001"

	provider := &mockRefundProvider{
		createResult: &razorpay.Refund{
			ID:        providerRefundID,
			PaymentID: providerPaymentID,
			Amount:    payment.AmountPaise,
			Currency:  payment.Currency,
			Status:    "processed",
		},
	}

	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})

	provider.afterCreate = func() {
		close(providerStarted)
		<-releaseProvider
	}

	service := newTestRefundService(t, repo, provider)

	firstWorkerDone := make(chan error, 1)

	go func() {
		firstWorkerDone <- service.ProcessPendingBatch(
			context.Background(),
		)
	}()

	// Wait until the first worker reaches the provider.
	select {
	case <-providerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first worker did not reach provider call")
	}

	// The second worker must not claim the refund.
	secondErr := service.ProcessPendingBatch(context.Background())
	if secondErr != nil {
		t.Fatalf("second worker returned error: %v", secondErr)
	}

	// Allow the first worker to finish.
	close(releaseProvider)

	select {
	case err := <-firstWorkerDone:
		if err != nil {
			t.Fatalf("first worker returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first worker did not finish")
	}

	createCalls, fetchCalls := provider.counts()

	if createCalls != 1 {
		t.Errorf(
			"expected exactly 1 CreateRefund call, got %d",
			createCalls,
		)
	}

	if fetchCalls != 0 {
		t.Errorf(
			"expected 0 FetchRefund calls, got %d",
			fetchCalls,
		)
	}

	var (
		status         string
		storedRefundID *string
		leaseToken     *string
	)

	err := pool.QueryRow(context.Background(), `
		SELECT
			status,
			provider_refund_id,
			processing_lease_token
		FROM payment_refunds
		WHERE id = $1
	`, refundID).Scan(
		&status,
		&storedRefundID,
		&leaseToken,
	)
	if err != nil {
		t.Fatalf("failed to query refund: %v", err)
	}

	if status != RefundStatusSucceeded {
		t.Errorf(
			"expected status %q, got %q",
			RefundStatusSucceeded,
			status,
		)
	}

	if storedRefundID == nil || *storedRefundID != providerRefundID {
		t.Errorf("unexpected provider refund ID: %v", storedRefundID)
	}

	if leaseToken != nil {
		t.Errorf(
			"expected processing lease to be cleared, got %v",
			leaseToken,
		)
	}
}

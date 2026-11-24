package payment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"staynest/internal/payment/razorpay"
)

type mockOrderProvider struct {
	mu sync.Mutex

	orders      []razorpay.Order
	createCalls int
	listCalls   int

	createErr error
	listErr   error

	persistBeforeCreateError bool

	createOrderResult *razorpay.Order

	createEntered chan struct{}
	allowCreate   chan struct{}
	createOnce    sync.Once
}

func (m *mockOrderProvider) CreateOrder(
	ctx context.Context,
	input razorpay.CreateOrderInput,
) (*razorpay.Order, error) {
	m.mu.Lock()
	m.createCalls++

	order := razorpay.Order{
		ID:       "order_test_123",
		Amount:   input.Amount,
		Currency: input.Currency,
		Receipt:  input.Receipt,
		Status:   "created",
	}

	if m.createOrderResult != nil {
		order = *m.createOrderResult
	}

	createErr := m.createErr

	if createErr == nil || m.persistBeforeCreateError {
		m.orders = append(m.orders, order)
	}

	m.mu.Unlock()

	if m.createEntered != nil {
		m.createOnce.Do(func() {
			close(m.createEntered)
		})
	}

	if m.allowCreate != nil {
		select {
		case <-m.allowCreate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if createErr != nil {
		return nil, createErr
	}

	return &order, nil
}

func (m *mockOrderProvider) ListOrdersByReceipt(
	ctx context.Context,
	receipt string,
) ([]razorpay.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.listCalls++

	if m.listErr != nil {
		return nil, m.listErr
	}

	var matching []razorpay.Order

	for _, order := range m.orders {
		if order.Receipt == receipt {
			matching = append(matching, order)
		}
	}

	return matching, nil
}

func (m *mockOrderProvider) counts() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.createCalls, m.listCalls
}

func newTestOrderService(
	t *testing.T,
	repo *Repository,
	provider *mockOrderProvider,
) *OrderService {
	t.Helper()

	service, err := NewOrderService(
		repo,
		provider,
		"rzp_test_key",
	)
	if err != nil {
		t.Fatalf("NewOrderService() error = %v", err)
	}

	return service
}

func TestOrderServiceCreateOrder(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	provider := &mockOrderProvider{}
	service := newTestOrderService(t, repo, provider)

	details, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if details == nil {
		t.Fatal("expected checkout details")
	}

	if details.PaymentID != payment.ID {
		t.Errorf("expected payment ID %q, got %q",
			payment.ID, details.PaymentID)
	}

	if details.ProviderOrderID != "order_test_123" {
		t.Errorf("unexpected provider order ID: %q",
			details.ProviderOrderID)
	}

	if details.AmountPaise != payment.AmountPaise {
		t.Errorf("expected amount %d, got %d",
			payment.AmountPaise, details.AmountPaise)
	}

	if details.Currency != CurrencyINR {
		t.Errorf("expected currency %q, got %q",
			CurrencyINR, details.Currency)
	}

	if details.KeyID != "rzp_test_key" {
		t.Errorf("expected Razorpay key ID, got %q", details.KeyID)
	}

	createCalls, listCalls := provider.counts()

	if createCalls != 1 {
		t.Errorf("expected 1 create call, got %d", createCalls)
	}

	if listCalls != 1 {
		t.Errorf("expected 1 lookup call, got %d", listCalls)
	}

	saved, err := repo.GetByIDForCustomer(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)
	if err != nil {
		t.Fatalf("failed to retrieve saved payment: %v", err)
	}

	if saved.ProviderOrderID == nil ||
		*saved.ProviderOrderID != "order_test_123" {
		t.Fatal("expected Razorpay order ID to be persisted")
	}
}

func TestOrderServiceRecoversExistingOrder(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	provider := &mockOrderProvider{
		orders: []razorpay.Order{
			{
				ID:       "order_existing_123",
				Amount:   payment.AmountPaise,
				Currency: payment.Currency,
				Receipt:  payment.ID,
				Status:   "created",
			},
		},
	}

	service := newTestOrderService(t, repo, provider)

	details, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if details.ProviderOrderID != "order_existing_123" {
		t.Fatalf("expected existing order ID, got %q",
			details.ProviderOrderID)
	}

	createCalls, _ := provider.counts()

	if createCalls != 0 {
		t.Errorf("expected no create calls, got %d", createCalls)
	}
}

func TestOrderServiceRecoversAfterCreateTimeout(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	provider := &mockOrderProvider{
		createErr:                errors.New("simulated network timeout"),
		persistBeforeCreateError: true,
	}

	service := newTestOrderService(t, repo, provider)

	details, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)
	if err != nil {
		t.Fatalf("expected recovery after timeout, got %v", err)
	}

	if details.ProviderOrderID != "order_test_123" {
		t.Fatalf("expected recovered order ID, got %q",
			details.ProviderOrderID)
	}

	createCalls, listCalls := provider.counts()

	if createCalls != 1 {
		t.Errorf("expected 1 create call, got %d", createCalls)
	}

	if listCalls != 2 {
		t.Errorf("expected 2 lookup calls, got %d", listCalls)
	}
}

func TestOrderServiceRetriesAfterNoOrderFound(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	provider := &mockOrderProvider{
		createErr: errors.New("simulated network timeout"),
	}

	service := newTestOrderService(t, repo, provider)

	_, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)

	if err == nil {
		t.Fatal("expected error when no provider order can be recovered")
	}

	// Simulate the next provider request succeeding.
	provider.mu.Lock()
	provider.createErr = nil
	provider.mu.Unlock()

	details, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)
	if err != nil {
		t.Fatalf("expected subsequent retry to succeed, got %v", err)
	}

	if details.ProviderOrderID != "order_test_123" {
		t.Fatalf("unexpected provider order ID: %q",
			details.ProviderOrderID)
	}

	createCalls, _ := provider.counts()

	if createCalls != 2 {
		t.Errorf("expected 2 create attempts, got %d", createCalls)
	}
}

func TestOrderServiceConcurrentRequests(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	provider := &mockOrderProvider{
		createEntered: make(chan struct{}),
		allowCreate:   make(chan struct{}),
	}

	service := newTestOrderService(t, repo, provider)

	type result struct {
		details *CheckoutDetails
		err     error
	}

	firstResult := make(chan result, 1)

	go func() {
		details, err := service.CreateOrder(
			context.Background(),
			payment.ID,
			fixture.customerID,
		)

		firstResult <- result{
			details: details,
			err:     err,
		}
	}()

	// Wait until the first request has acquired its lease
	// and reached the provider.
	select {
	case <-provider.createEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach provider")
	}

	// The second request must not create another order.
	_, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)

	if !errors.Is(err, ErrOrderCreationInProgress) {
		t.Fatalf("expected ErrOrderCreationInProgress, got %v", err)
	}

	close(provider.allowCreate)

	first := <-firstResult

	if first.err != nil {
		t.Fatalf("first request failed: %v", first.err)
	}

	if first.details == nil {
		t.Fatal("expected checkout details from first request")
	}

	createCalls, _ := provider.counts()

	if createCalls != 1 {
		t.Errorf("expected exactly 1 provider create call, got %d",
			createCalls)
	}
}

func TestOrderServiceRejectsMultipleProviderOrders(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	provider := &mockOrderProvider{
		orders: []razorpay.Order{
			{
				ID:       "order_first",
				Amount:   payment.AmountPaise,
				Currency: payment.Currency,
				Receipt:  payment.ID,
			},
			{
				ID:       "order_second",
				Amount:   payment.AmountPaise,
				Currency: payment.Currency,
				Receipt:  payment.ID,
			},
		},
	}

	service := newTestOrderService(t, repo, provider)

	_, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)

	if !errors.Is(err, ErrMultipleProviderOrdersFound) {
		t.Fatalf(
			"expected ErrMultipleProviderOrdersFound, got %v",
			err,
		)
	}

	createCalls, _ := provider.counts()

	if createCalls != 0 {
		t.Fatalf("expected no create calls, got %d", createCalls)
	}
}

func TestOrderServiceRejectsMismatchedProviderOrder(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*razorpay.Order)
		wantErr error
	}{
		{
			name: "receipt mismatch",
			modify: func(order *razorpay.Order) {
				order.Receipt = "wrong-receipt"
			},
			wantErr: ErrProviderOrderMismatch,
		},
		{
			name: "amount mismatch",
			modify: func(order *razorpay.Order) {
				order.Amount++
			},
			wantErr: ErrPaymentAmountMismatch,
		},
		{
			name: "currency mismatch",
			modify: func(order *razorpay.Order) {
				order.Currency = "USD"
			},
			wantErr: ErrPaymentCurrencyMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, pool := setupPaymentRepository(t)
			fixture := createPaymentTestFixture(t, pool)
			payment := createPaymentForLeaseTest(t, repo, fixture)

			invalidOrder := razorpay.Order{
				ID:       "order_invalid_123",
				Amount:   payment.AmountPaise,
				Currency: payment.Currency,
				Receipt:  payment.ID,
				Status:   "created",
			}

			tt.modify(&invalidOrder)

			provider := &mockOrderProvider{
				createOrderResult: &invalidOrder,
			}

			service := newTestOrderService(t, repo, provider)

			_, err := service.CreateOrder(
				context.Background(),
				payment.ID,
				fixture.customerID,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf(
					"expected %v, got %v",
					tt.wantErr,
					err,
				)
			}

			saved, getErr := repo.GetByIDForCustomer(
				context.Background(),
				payment.ID,
				fixture.customerID,
			)
			if getErr != nil {
				t.Fatalf("failed to retrieve payment: %v", getErr)
			}

			if saved.ProviderOrderID != nil {
				t.Fatal("invalid provider order must not be persisted")
			}
		})
	}
}

func TestOrderServiceHandlesProviderLookupFailure(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)
	payment := createPaymentForLeaseTest(t, repo, fixture)

	provider := &mockOrderProvider{
		listErr: errors.New("simulated Razorpay lookup failure"),
	}

	service := newTestOrderService(t, repo, provider)

	_, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)

	if err == nil {
		t.Fatal("expected provider lookup failure")
	}

	createCalls, _ := provider.counts()

	if createCalls != 0 {
		t.Fatalf(
			"must not create an order when lookup fails, got %d calls",
			createCalls,
		)
	}

	// Simulate Razorpay becoming available again.
	provider.mu.Lock()
	provider.listErr = nil
	provider.mu.Unlock()

	details, err := service.CreateOrder(
		context.Background(),
		payment.ID,
		fixture.customerID,
	)

	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}

	if details.ProviderOrderID != "order_test_123" {
		t.Fatalf("unexpected provider order ID: %q",
			details.ProviderOrderID)
	}
}

func TestOrderServiceRecoversAfterPersistenceFailure(t *testing.T) {
	repo, pool := setupPaymentRepository(t)
	fixture := createPaymentTestFixture(t, pool)

	payment := createPaymentForLeaseTest(t, repo, fixture)
	blockingPayment := createPaymentForLeaseTest(t, repo, fixture)

	ctx := context.Background()

	_, err := pool.Exec(ctx, `
        UPDATE payments
        SET provider_order_id = $1
        WHERE id = $2
    `, "order_test_123", blockingPayment.ID)

	if err != nil {
		t.Fatalf("failed to create provider order ID conflict: %v", err)
	}

	provider := &mockOrderProvider{}
	service := newTestOrderService(t, repo, provider)

	// Provider creation succeeds, but PostgreSQL rejects
	// the duplicate provider_order_id.
	_, err = service.CreateOrder(
		ctx,
		payment.ID,
		fixture.customerID,
	)

	if err == nil {
		t.Fatal("expected database persistence failure")
	}

	saved, getErr := repo.GetByIDForCustomer(
		ctx,
		payment.ID,
		fixture.customerID,
	)

	if getErr != nil {
		t.Fatalf("failed to retrieve payment: %v", getErr)
	}

	if saved.ProviderOrderID != nil {
		t.Fatal("order ID should not have been persisted")
	}

	// Remove the conflicting order ID.
	_, err = pool.Exec(ctx, `
        UPDATE payments
        SET provider_order_id = NULL
        WHERE id = $1
    `, blockingPayment.ID)

	if err != nil {
		t.Fatalf("failed to remove provider order ID conflict: %v", err)
	}

	// Retry. The provider already has the order, so it should
	// be recovered rather than created again.
	details, err := service.CreateOrder(
		ctx,
		payment.ID,
		fixture.customerID,
	)

	if err != nil {
		t.Fatalf("expected order recovery, got %v", err)
	}

	if details.ProviderOrderID != "order_test_123" {
		t.Fatalf("unexpected provider order ID: %q",
			details.ProviderOrderID)
	}

	createCalls, _ := provider.counts()

	if createCalls != 1 {
		t.Fatalf(
			"expected exactly one provider create call, got %d",
			createCalls,
		)
	}
}

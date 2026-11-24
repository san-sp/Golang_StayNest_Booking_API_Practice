package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"staynest/internal/payment/razorpay"
)

const (
	defaultRefundBatchSize     = 10
	defaultRefundLeaseDuration = 60 * time.Second
	defaultRefundRetryDelay    = 5 * time.Minute
)

// RefundProvider defines the Razorpay operations required by RefundService.
type RefundProvider interface {
	CreateRefund(
		ctx context.Context,
		input *razorpay.CreateRefundInput,
	) (*razorpay.Refund, error)

	FetchRefund(
		ctx context.Context,
		refundID string,
	) (*razorpay.Refund, error)
}

type RefundService struct {
	repo          *Repository
	provider      RefundProvider
	batchSize     int
	leaseDuration time.Duration
	retryDelay    time.Duration
}

func NewRefundService(
	repo *Repository,
	provider RefundProvider,
	batchSize int,
	leaseDuration time.Duration,
	retryDelay time.Duration,
) (*RefundService, error) {
	if repo == nil || provider == nil {
		return nil, ErrInvalidRefundServiceConfig
	}

	if batchSize <= 0 {
		return nil, ErrInvalidRefundBatchSize
	}

	if leaseDuration <= 0 {
		return nil, ErrInvalidRefundLeaseDuration
	}

	if retryDelay <= 0 {
		return nil, ErrInvalidRefundRetryDelay
	}

	return &RefundService{
		repo:          repo,
		provider:      provider,
		batchSize:     batchSize,
		leaseDuration: leaseDuration,
		retryDelay:    retryDelay,
	}, nil
}

// ProcessPendingBatch processes refunds that are ready for processing.
func (s *RefundService) ProcessPendingBatch(ctx context.Context) error {
	refunds, err := s.repo.ClaimPendingRefunds(
		ctx,
		s.batchSize,
		s.leaseDuration,
	)
	if err != nil {
		return fmt.Errorf("claim pending refunds: %w", err)
	}

	return s.processClaimedRefunds(ctx, refunds)
}

// ProcessExpiredBatch recovers refunds whose processing leases expired.
func (s *RefundService) ProcessExpiredBatch(ctx context.Context) error {
	refunds, err := s.repo.ClaimExpiredRefunds(
		ctx,
		s.batchSize,
		s.leaseDuration,
	)
	if err != nil {
		return fmt.Errorf("claim expired refunds: %w", err)
	}

	return s.processClaimedRefunds(ctx, refunds)
}

// processClaimedRefunds processes each claimed refund independently.
func (s *RefundService) processClaimedRefunds(
	ctx context.Context,
	refunds []Refund,
) error {
	var batchErr error

	for _, refund := range refunds {
		if err := ctx.Err(); err != nil {
			return errors.Join(batchErr, err)
		}

		if err := s.processRefund(ctx, refund); err != nil {
			batchErr = errors.Join(
				batchErr,
				fmt.Errorf("process refund %s: %w", refund.ID, err),
			)
		}
	}

	return batchErr
}

// processRefund creates or reconciles a refund with Razorpay.
func (s *RefundService) processRefund(
	ctx context.Context,
	refund Refund,
) error {
	if refund.ProcessingLeaseToken == nil ||
		strings.TrimSpace(*refund.ProcessingLeaseToken) == "" {
		return ErrRefundLeaseLost
	}

	leaseToken := *refund.ProcessingLeaseToken

	var (
		providerRefund *razorpay.Refund
		err            error
	)

	if refund.ProviderRefundID != nil &&
		strings.TrimSpace(*refund.ProviderRefundID) != "" {

		// A provider refund already exists. Reconcile it instead
		// of creating another refund.
		providerRefund, err = s.provider.FetchRefund(
			ctx,
			*refund.ProviderRefundID,
		)
		if err != nil {
			return s.handleProviderError(
				ctx,
				refund,
				leaseToken,
				err,
			)
		}

		if err := validateProviderRefund(refund, providerRefund); err != nil {
			return s.markRefundForReview(
				ctx,
				refund,
				leaseToken,
				err.Error(),
			)
		}

		if providerRefund.ID != *refund.ProviderRefundID {
			return s.markRefundForReview(
				ctx,
				refund,
				leaseToken,
				"provider returned a different refund ID",
			)
		}
	} else {
		// No provider refund ID exists. Create the refund using
		// the original idempotency key.
		providerRefund, err = s.provider.CreateRefund(
			ctx,
			&razorpay.CreateRefundInput{
				PaymentID:      refund.ProviderPaymentID,
				AmountPaise:    refund.AmountPaise,
				IdempotencyKey: refund.IdempotencyKey,
			},
		)
		if err != nil {
			return s.handleProviderError(
				ctx,
				refund,
				leaseToken,
				err,
			)
		}

		if err := validateProviderRefund(refund, providerRefund); err != nil {
			return s.markRefundForReview(
				ctx,
				refund,
				leaseToken,
				err.Error(),
			)
		}

		// Persist the provider refund ID before changing the
		// local refund status. This makes recovery safe if the
		// worker crashes between these operations.
		if err := s.repo.RecordProviderRefundID(
			ctx,
			refund.ID,
			leaseToken,
			providerRefund.ID,
		); err != nil {
			return fmt.Errorf("record provider refund ID: %w", err)
		}
	}

	return s.reconcileProviderRefund(
		ctx,
		refund,
		leaseToken,
		providerRefund,
	)
}

// validateProviderRefund verifies that the provider response
// belongs to the expected payment and refund amount.
func validateProviderRefund(
	expected Refund,
	actual *razorpay.Refund,
) error {
	if actual == nil {
		return ErrInvalidProviderRefund
	}

	if strings.TrimSpace(actual.ID) == "" ||
		strings.TrimSpace(actual.PaymentID) == "" ||
		strings.TrimSpace(actual.Currency) == "" {
		return ErrInvalidProviderRefund
	}

	if actual.PaymentID != expected.ProviderPaymentID {
		return fmt.Errorf(
			"%w: provider payment ID mismatch",
			ErrInvalidProviderRefund,
		)
	}

	if actual.Amount != expected.AmountPaise {
		return fmt.Errorf(
			"%w: refund amount mismatch",
			ErrInvalidProviderRefund,
		)
	}

	if actual.Currency != expected.Currency {
		return fmt.Errorf(
			"%w: refund currency mismatch",
			ErrInvalidProviderRefund,
		)
	}

	return nil
}

// reconcileProviderRefund updates our database according to
// the refund status reported by Razorpay.
func (s *RefundService) reconcileProviderRefund(
	ctx context.Context,
	refund Refund,
	leaseToken string,
	providerRefund *razorpay.Refund,
) error {
	if providerRefund == nil {
		return s.markRefundForReview(
			ctx,
			refund,
			leaseToken,
			ErrInvalidProviderRefund.Error(),
		)
	}

	switch providerRefund.Status {
	case "processed":
		return s.repo.MarkRefundSucceeded(
			ctx,
			refund.ID,
			leaseToken,
			providerRefund.ID,
		)

	case "pending":
		return s.scheduleRefundRetry(
			ctx,
			refund,
			leaseToken,
			"provider refund is still pending",
		)

	case "failed":
		return s.repo.MarkRefundFailed(
			ctx,
			refund.ID,
			leaseToken,
			"provider reported that the refund failed",
		)

	default:
		return s.markRefundForReview(
			ctx,
			refund,
			leaseToken,
			fmt.Sprintf(
				"unknown provider refund status: %q",
				providerRefund.Status,
			),
		)
	}
}

// handleProviderError distinguishes transient errors from
// errors that need manual investigation.
func (s *RefundService) handleProviderError(
	ctx context.Context,
	refund Refund,
	leaseToken string,
	providerErr error,
) error {
	var apiErr *razorpay.APIError

	if errors.As(providerErr, &apiErr) {
		if apiErr.StatusCode >= 400 &&
			apiErr.StatusCode < 500 &&
			apiErr.StatusCode != 429 {
			return errors.Join(
				providerErr,
				s.markRefundForReview(
					ctx,
					refund,
					leaseToken,
					providerErr.Error(),
				),
			)
		}
	}

	return errors.Join(
		providerErr,
		s.scheduleRefundRetry(
			ctx,
			refund,
			leaseToken,
			providerErr.Error(),
		),
	)
}

func (s *RefundService) scheduleRefundRetry(
	ctx context.Context,
	refund Refund,
	leaseToken string,
	reason string,
) error {
	nextAttemptAt := time.Now().UTC().Add(s.retryDelay)

	return s.repo.ScheduleRefundRetry(
		ctx,
		refund.ID,
		leaseToken,
		reason,
		nextAttemptAt,
	)
}

func (s *RefundService) markRefundForReview(
	ctx context.Context,
	refund Refund,
	leaseToken string,
	reason string,
) error {
	return s.repo.MarkRefundReviewRequired(
		ctx,
		refund.ID,
		leaseToken,
		reason,
	)
}

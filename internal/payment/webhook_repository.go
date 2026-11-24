package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RecordWebhookEvent inserts a new Razorpay event or retrieves the
// original event when Razorpay delivers the same event again.
//
// The original payload is never overwritten.
func (r *Repository) RecordWebhookEvent(
	ctx context.Context,
	providerEventID string,
	eventType string,
	payload json.RawMessage,
) (*WebhookEvent, bool, error) {
	providerEventID = strings.TrimSpace(providerEventID)
	eventType = strings.TrimSpace(eventType)

	if providerEventID == "" || len(providerEventID) > 255 {
		return nil, false, ErrInvalidWebhookPayload
	}

	if eventType == "" || len(eventType) > 100 {
		return nil, false, ErrInvalidWebhookPayload
	}

	if !json.Valid(payload) {
		return nil, false, ErrInvalidWebhookPayload
	}

	event, err := scanWebhookEvent(r.pool.QueryRow(
		ctx,
		`
		INSERT INTO payment_webhook_events (
			provider,
			provider_event_id,
			event_type,
			payload
		)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (provider, provider_event_id) DO NOTHING
		RETURNING
			id::text,
			payment_id::text,
			provider,
			provider_event_id,
			event_type,
			processing_status,
			payload,
			processed_at,
			created_at,
			updated_at
		`,
		ProviderRazorpay,
		providerEventID,
		eventType,
		[]byte(payload),
	))

	if err == nil {
		return event, true, nil
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("insert webhook event: %w", err)
	}

	// This event ID already exists. Retrieve the original event.
	// PostgreSQL JSONB equality ignores insignificant whitespace
	// and object-key ordering.
	var payloadMatches bool

	event, err = scanWebhookEventWithPayloadMatch(
		r.pool.QueryRow(
			ctx,
			`
        SELECT
            id::text,
            payment_id::text,
            provider,
            provider_event_id,
            event_type,
            processing_status,
            payload,
            processed_at,
            created_at,
            updated_at,
            payload = $3::jsonb
        FROM payment_webhook_events
        WHERE provider = $1
          AND provider_event_id = $2
        `,
			ProviderRazorpay,
			providerEventID,
			[]byte(payload),
		),
		&payloadMatches,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrWebhookEventNotFound
		}

		return nil, false, fmt.Errorf("retrieve existing webhook event: %w", err)
	}

	// A reused event ID with a different event type or payload
	// must never be processed as the original event.
	if !payloadMatches || event.EventType != eventType {
		return nil, false, ErrWebhookEventConflict
	}

	return event, false, nil
}

// MarkWebhookEventProcessed marks an event as successfully processed.
// An empty paymentID is allowed for events that do not concern a
// locally recognized payment.
func (r *Repository) MarkWebhookEventProcessed(
	ctx context.Context,
	providerEventID string,
	paymentID string,
) error {
	providerEventID = strings.TrimSpace(providerEventID)
	paymentID = strings.TrimSpace(paymentID)

	if providerEventID == "" {
		return ErrInvalidWebhookPayload
	}

	tag, err := r.pool.Exec(
		ctx,
		`
		UPDATE payment_webhook_events
		SET payment_id = COALESCE(NULLIF($1, '')::uuid, payment_id),
		    processing_status = $2,
		    processed_at = COALESCE(processed_at, $3),
		    updated_at = $3
		WHERE provider = $4
		  AND provider_event_id = $5
		`,
		paymentID,
		WebhookStatusProcessed,
		r.now().UTC(),
		ProviderRazorpay,
		providerEventID,
	)

	if err != nil {
		return fmt.Errorf("mark webhook event processed: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return ErrWebhookEventNotFound
	}

	return nil
}

// MarkWebhookEventFailed records a processing failure.
// It never downgrades an event that has already been processed.
func (r *Repository) MarkWebhookEventFailed(
	ctx context.Context,
	providerEventID string,
) error {
	providerEventID = strings.TrimSpace(providerEventID)

	if providerEventID == "" {
		return ErrInvalidWebhookPayload
	}

	tag, err := r.pool.Exec(
		ctx,
		`
		UPDATE payment_webhook_events
		SET processing_status = $1,
		    updated_at = $2
		WHERE provider = $3
		  AND provider_event_id = $4
		  AND processing_status <> $5
		`,
		WebhookStatusFailed,
		r.now().UTC(),
		ProviderRazorpay,
		providerEventID,
		WebhookStatusProcessed,
	)

	if err != nil {
		return fmt.Errorf("mark webhook event failed: %w", err)
	}

	if tag.RowsAffected() == 1 {
		return nil
	}

	// Zero affected rows can mean either an already processed event
	// or an event that does not exist.
	var exists bool

	err = r.pool.QueryRow(
		ctx,
		`
		SELECT EXISTS (
			SELECT 1
			FROM payment_webhook_events
			WHERE provider = $1
			  AND provider_event_id = $2
		)
		`,
		ProviderRazorpay,
		providerEventID,
	).Scan(&exists)

	if err != nil {
		return fmt.Errorf("check webhook event existence: %w", err)
	}

	if !exists {
		return ErrWebhookEventNotFound
	}

	// Already processed: preserve the successful state.
	return nil
}

// scanWebhookEvent maps a PostgreSQL webhook event row into the domain model.
// The final optional payloadMatches column is used only by duplicate lookup.

func scanWebhookEvent(row pgx.Row) (*WebhookEvent, error) {
	return scanWebhookEventWithPayloadMatch(row, nil)
}

func scanWebhookEventWithPayloadMatch(
	row pgx.Row,
	payloadMatches *bool,
) (*WebhookEvent, error) {
	var (
		event       WebhookEvent
		paymentID   pgtype.Text
		processedAt pgtype.Timestamptz
		payload     []byte
	)

	dest := []any{
		&event.ID,
		&paymentID,
		&event.Provider,
		&event.ProviderEventID,
		&event.EventType,
		&event.ProcessingStatus,
		&payload,
		&processedAt,
		&event.CreatedAt,
		&event.UpdatedAt,
	}

	if payloadMatches != nil {
		dest = append(dest, payloadMatches)
	}

	if err := row.Scan(dest...); err != nil {
		return nil, err
	}

	if paymentID.Valid {
		event.PaymentID = &paymentID.String
	}

	if processedAt.Valid {
		event.ProcessedAt = &processedAt.Time
	}

	event.Payload = json.RawMessage(payload)

	return &event, nil
}

// Keep time imported for consistency with repository time handling.
var _ time.Time

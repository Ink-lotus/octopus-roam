package relay

import (
	"context"

	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// preserveDeliveryCapabilities restores capabilities lost by a protocol adapter.
// Prefer an adapter's own implementation, and expose only capabilities that
// actually exist. Iteration and Close always belong to the adapted stream.
func preserveDeliveryCapabilities[T, R any](source streams.Stream[T], adapted streams.Stream[R]) streams.Stream[R] {
	ack, ok := adapted.(streams.DeliveryAcknowledger)
	if !ok {
		ack, _ = source.(streams.DeliveryAcknowledger)
	}
	boundary, ok := adapted.(streams.DeliveryBoundary)
	if !ok {
		boundary, _ = source.(streams.DeliveryBoundary)
	}
	waiter, ok := adapted.(streams.FinalizationWaiter)
	if !ok {
		waiter, _ = source.(streams.FinalizationWaiter)
	}

	switch {
	case ack != nil && boundary != nil && waiter != nil:
		return &struct {
			streams.Stream[R]
			streams.DeliveryAcknowledger
			streams.DeliveryBoundary
			streams.FinalizationWaiter
		}{adapted, ack, boundary, waiter}
	case ack != nil && boundary != nil:
		return &struct {
			streams.Stream[R]
			streams.DeliveryAcknowledger
			streams.DeliveryBoundary
		}{adapted, ack, boundary}
	case ack != nil && waiter != nil:
		return &struct {
			streams.Stream[R]
			streams.DeliveryAcknowledger
			streams.FinalizationWaiter
		}{adapted, ack, waiter}
	case boundary != nil && waiter != nil:
		return &struct {
			streams.Stream[R]
			streams.DeliveryBoundary
			streams.FinalizationWaiter
		}{adapted, boundary, waiter}
	case ack != nil:
		return &struct {
			streams.Stream[R]
			streams.DeliveryAcknowledger
		}{adapted, ack}
	case boundary != nil:
		return &struct {
			streams.Stream[R]
			streams.DeliveryBoundary
		}{adapted, boundary}
	case waiter != nil:
		return &struct {
			streams.Stream[R]
			streams.FinalizationWaiter
		}{adapted, waiter}
	default:
		return adapted
	}
}

// deliverStreamEvent confirms a successful client terminator, independently of
// provider completion. The caller checks short writes and includes Flush in
// write. Cancellation after a completed write cannot undo that delivery.
func deliverStreamEvent(ctx context.Context, stream streams.Stream[*httpclient.StreamEvent], terminal bool, write func() error) error {
	deliver := func() (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := write(); err != nil {
			return false, err
		}
		return terminal, nil
	}
	if boundary, ok := stream.(streams.DeliveryBoundary); ok {
		return boundary.Deliver(ctx, deliver)
	}
	last, err := deliver()
	if err == nil && last {
		if ack, ok := stream.(streams.DeliveryAcknowledger); ok {
			ack.AcknowledgeDelivery()
		}
	}
	return err
}

// Call only after Close, request cancellation and connection/capacity release.
// A source with asynchronous accounting owns its worker's shutdown; ordinary
// HTTP decoders have no waiter and need no extra goroutine or wait here.
func waitForStreamFinalization(stream streams.Stream[*httpclient.StreamEvent]) {
	if waiter, ok := stream.(streams.FinalizationWaiter); ok {
		waiter.WaitForFinalization()
	}
}

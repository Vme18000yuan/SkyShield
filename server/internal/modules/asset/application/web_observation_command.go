package application

import (
	"context"
	"errors"

	contractresults "github.com/yyhuni/lunafox/contracts/results"
	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
)

type sparseWebObservationStore interface {
	MergeWebObservationContext(context.Context, int, string, assetdomain.SparseWebObservation) error
}

func mergeWebObservation(ctx context.Context, lookup AssetCommandTargetLookup, store any, targetID int, rawURL string, observation assetdomain.SparseWebObservation) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := contractresults.ValidateObservedAssetURL(rawURL); err != nil {
		return err
	}
	target, err := lookup.GetActiveByIDContext(ctx, targetID)
	if err != nil {
		return err
	}
	if !assetdomain.IsURLMatchTarget(rawURL, *target) {
		return errors.New("web observation is outside the selected target")
	}
	writer, ok := store.(sparseWebObservationStore)
	if !ok {
		return errors.New("sparse web observation store is required")
	}
	return writer.MergeWebObservationContext(ctx, targetID, rawURL, observation)
}

func (s *WebsiteCommandService) MergeWebObservationContext(ctx context.Context, targetID int, rawURL string, observation assetdomain.SparseWebObservation) error {
	return mergeWebObservation(ctx, s.targetLookup, s.store, targetID, rawURL, observation)
}

func (s *EndpointCommandService) MergeWebObservationContext(ctx context.Context, targetID int, rawURL string, observation assetdomain.SparseWebObservation) error {
	lookup, ok := s.targetLookup.(AssetCommandTargetLookup)
	if !ok {
		return errors.New("context-aware endpoint target lookup is required")
	}
	return mergeWebObservation(ctx, lookup, s.store, targetID, rawURL, observation)
}

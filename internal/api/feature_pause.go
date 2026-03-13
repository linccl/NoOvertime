package api

import (
	"net/http"

	apperrors "noovertime/internal/errors"
)

const featurePausedCode = "FEATURE_PAUSED"

func pausedEndpointHandler(feature string) appHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := ensurePostMethod(r); err != nil {
			return err
		}
		return featurePaused(feature)
	}
}

func featurePaused(feature string) error {
	return apperrors.New(http.StatusGone, featurePausedCode, feature+" is paused in token-only mode")
}

package resilience

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Classification struct {
	Kind       FailureKind
	Retryable  bool
	BreakerKey string
	Cooldown   time.Duration
	Reason     string
}

func Classify(status int, body []byte, header http.Header, err error, requestContextErr error) Classification {
	if requestContextErr != nil || errors.Is(err, context.Canceled) {
		return Classification{Kind: FailureClientCancelled, Retryable: false, Reason: "client_cancelled"}
	}
	if err != nil {
		msg := strings.ToLower(err.Error())
		for _, p := range []string{"cannot translate", "has no protocol compatible", "connector not found or disabled", "provider not found or disabled", "unsupported api"} {
			if strings.Contains(msg, p) {
				return Classification{Kind: FailureInvalidRequest, Retryable: false, Reason: "local_request_rejected"}
			}
		}
		if strings.Contains(msg, "credential") || strings.Contains(msg, "secret") {
			return Classification{Kind: FailureAuthentication, Retryable: false, Cooldown: 5 * time.Minute, Reason: "credential_unavailable"}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return Classification{Kind: FailureTimeout, Retryable: true, Reason: "upstream_deadline"}
		}
		var ne net.Error
		if errors.As(err, &ne) {
			if ne.Timeout() {
				return Classification{Kind: FailureTimeout, Retryable: true, Reason: "network_timeout"}
			}
			return Classification{Kind: FailureNetwork, Retryable: true, Reason: "network_error"}
		}
		return Classification{Kind: FailureNetwork, Retryable: true, Reason: "upstream_transport_error"}
	}

	text := strings.ToLower(string(body))
	cooldown := retryAfter(header)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return Classification{Kind: FailureAuthentication, Retryable: false, Cooldown: maxDuration(cooldown, 5*time.Minute), Reason: "authentication_rejected"}
	case http.StatusTooManyRequests:
		quotaWords := []string{"insufficient_quota", "quota exceeded", "quota_exceeded", "billing", "credit balance", "monthly limit", "daily limit", "usage limit", "resource_exhausted"}
		for _, p := range quotaWords {
			if strings.Contains(text, p) {
				return Classification{Kind: FailureQuotaExhausted, Retryable: true, Cooldown: maxDuration(cooldown, 5*time.Minute), Reason: "quota_exhausted"}
			}
		}
		return Classification{Kind: FailureRateLimit, Retryable: true, Cooldown: cooldown, Reason: "rate_limited"}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return Classification{Kind: FailureTimeout, Retryable: true, Cooldown: cooldown, Reason: "upstream_timeout"}
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return Classification{Kind: FailureProvider5xx, Retryable: true, Cooldown: cooldown, Reason: "provider_5xx"}
	case http.StatusNotFound, http.StatusBadRequest, http.StatusUnprocessableEntity:
		modelWords := []string{"model not found", "model_not_found", "model does not exist", "unknown model", "unsupported model", "model is not available", "model unavailable", "no such model"}
		for _, p := range modelWords {
			if strings.Contains(text, p) {
				return Classification{Kind: FailureModelUnavailable, Retryable: true, Cooldown: maxDuration(cooldown, 5*time.Minute), Reason: "model_unavailable"}
			}
		}
		return Classification{Kind: FailureInvalidRequest, Retryable: false, Reason: "invalid_request"}
	}
	if status >= 500 {
		return Classification{Kind: FailureProvider5xx, Retryable: true, Cooldown: cooldown, Reason: "provider_5xx"}
	}
	if status >= 400 {
		return Classification{Kind: FailureInvalidRequest, Retryable: false, Reason: "client_or_parameter_error"}
	}
	return Classification{}
}

func retryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if sec, err := strconv.Atoi(raw); err == nil && sec > 0 {
		if sec > 3600 {
			sec = 3600
		}
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(raw); err == nil {
		d := time.Until(t)
		if d > 0 {
			if d > time.Hour {
				d = time.Hour
			}
			return d
		}
	}
	return 0
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

package quotacost

import (
	"net/http"
	"path/filepath"
	"testing"

	"omnibridge/internal/config"
	"omnibridge/internal/storage"
)

func TestPriceEstimateAndPersistence(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := New(db, config.QuotaCostConfig{Enabled: true, LowQuotaPercent: 10, PricingRefreshHours: 24})
	if err := e.UpsertPrice(Price{Provider: "p", Model: "m", InputUSDPerMillion: 2, OutputUSDPerMillion: 8}); err != nil {
		t.Fatal(err)
	}
	got := e.Estimate("p", "m", 1_000_000, 500_000, 0)
	if !got.Known || got.CostUSD != 6 {
		t.Fatalf("unexpected estimate: %+v", got)
	}
	e2 := New(db, config.QuotaCostConfig{Enabled: true, LowQuotaPercent: 10, PricingRefreshHours: 24})
	if _, ok := e2.Price("p", "m"); !ok {
		t.Fatal("price did not persist")
	}
}

func TestObserveHeadersBuildsVerifiedQuota(t *testing.T) {
	e := New(nil, config.QuotaCostConfig{Enabled: true, LowQuotaPercent: 10, PricingRefreshHours: 24})
	h := http.Header{}
	h.Set("X-RateLimit-Remaining-Requests", "5")
	h.Set("X-RateLimit-Limit-Requests", "100")
	e.ObserveHeaders("api:p", h)
	q, ok := e.Quota("api:p")
	if !ok || !q.RemainingKnown || q.RemainingPercent != 5 || !q.LowQuota || q.Confidence != "verified" {
		t.Fatalf("unexpected quota: %+v", q)
	}
}

func TestObserveOfficialAccountUsage(t *testing.T) {
	e := New(nil, config.QuotaCostConfig{Enabled: true, LowQuotaPercent: 10, PricingRefreshHours: 24})
	ok := e.ObserveAccountUsage("acct", map[string]any{"rate_limits": map[string]any{"primary": map[string]any{"usedPercent": 75.0}}})
	if !ok {
		t.Fatal("not observed")
	}
	q, _ := e.Quota("acct")
	if q.RemainingPercent != 25 || q.Confidence != "verified" {
		t.Fatalf("bad quota %+v", q)
	}
}

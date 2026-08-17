package server

import (
	"context"
	"testing"
	"time"
)

func TestExtractUsageDetailedReadsYandexCachedTokens(t *testing.T) {
	in, out, total, cached := extractUsageDetailed([]byte(`{"usage":{"input_tokens":1200,"output_tokens":80,"total_tokens":1280,"input_tokens_details":{"cached_tokens":900}}}`))
	if in != 1200 || out != 80 || total != 1280 || cached != 900 {
		t.Fatalf("unexpected usage in=%d out=%d total=%d cached=%d", in, out, total, cached)
	}
	in, out, total, cached = extractUsageDetailed([]byte(`{"usage":{"prompt_tokens":500,"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":300}}}`))
	if in != 500 || out != 40 || total != 540 || cached != 300 {
		t.Fatalf("unexpected chat usage in=%d out=%d total=%d cached=%d", in, out, total, cached)
	}
}

func TestAcquireSlotRejectsWhenCapacityIsBusy(t *testing.T) {
	slots := make(chan struct{}, 1)
	release, ok := acquireSlot(context.Background(), slots, 10*time.Millisecond)
	if !ok {
		t.Fatal("first slot was not acquired")
	}
	defer release()
	if second, ok := acquireSlot(context.Background(), slots, 0); ok {
		second()
		t.Fatal("busy slot unexpectedly acquired")
	}
}

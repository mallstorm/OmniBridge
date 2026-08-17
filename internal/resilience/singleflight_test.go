package resilience

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleFlightCollapsesConcurrentWork(t *testing.T) {
	g := NewSingleFlight()
	var calls atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, err, _ := g.Do(context.Background(), "models", func() (any, error) {
				calls.Add(1)
				time.Sleep(30 * time.Millisecond)
				return "ok", nil
			})
			if err != nil || v != "ok" {
				t.Errorf("v=%v err=%v", v, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("calls=%d want 1", calls.Load())
	}
}

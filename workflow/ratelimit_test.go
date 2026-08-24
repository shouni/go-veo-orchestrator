package workflow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// TestRateLimiterSpacesCalls pins that the limiter actually enforces the interval.
// This is the setting ap-mv exposes as KEYFRAME_RATE_INTERVAL; before it was wired it was
// read by nothing, so a configured 60s spacing silently became "as fast as generation allows".
func TestRateLimiterSpacesCalls(t *testing.T) {
	// バブル内は仮想時計なので、実時間を消費せず、かつ経過時間が誤差なく決まります。
	// 「少なくとも 2 周期」ではなく「ちょうど 2 周期」を期待できます。
	synctest.Test(t, func(t *testing.T) {
		const interval = 30 * time.Millisecond
		l := newRateLimiter(interval)
		ctx := context.Background()

		start := time.Now()
		for range 3 {
			if err := l.wait(ctx); err != nil {
				t.Fatalf("wait() error = %v", err)
			}
		}
		// 1回目は即時、2・3回目がそれぞれ interval 待つ。
		if elapsed := time.Since(start); elapsed != 2*interval {
			t.Errorf("3 calls took %v, want exactly %v", elapsed, 2*interval)
		}
	})
}

// TestRateLimiterNilMeansNoLimit pins that a zero interval yields a nil limiter that callers
// can use without a branch. Config.RateInterval is deliberately not defaulted, so 0 has to keep
// meaning "no limit" rather than becoming an accidental delay.
func TestRateLimiterNilMeansNoLimit(t *testing.T) {
	if l := newRateLimiter(0); l != nil {
		t.Fatalf("newRateLimiter(0) = %v, want nil", l)
	}
	if l := newRateLimiter(-time.Second); l != nil {
		t.Fatalf("newRateLimiter(negative) = %v, want nil", l)
	}

	synctest.Test(t, func(t *testing.T) {
		var l *rateLimiter // nil レシーバでも安全に呼べること
		start := time.Now()
		if err := l.wait(context.Background()); err != nil {
			t.Fatalf("wait() on nil = %v", err)
		}
		// 仮想時計では「一切待っていない」を 0 で表せます。
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("nil limiter waited %v, want no delay", elapsed)
		}
	})
}

// TestRateLimiterRespectsCancel pins that a queued caller gives up when its context ends,
// instead of holding the pipeline past a shutdown.
func TestRateLimiterRespectsCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newRateLimiter(time.Hour)
		ctx := context.Background()

		if err := l.wait(ctx); err != nil { // 1回目で枠を消費し、2回目を長時間待たせる
			t.Fatalf("first wait() error = %v", err)
		}

		cancelCtx, cancel := context.WithCancel(ctx)
		waitErr := make(chan error, 1)
		go func() {
			waitErr <- l.wait(cancelCtx)
		}()

		// 2回目が実際に枠待ちへ入ってからキャンセルする。Wait はそのゴルーチンが
		// 継続的にブロックした時点で返るので、待ち時間の見積もりが要りません。
		synctest.Wait()
		cancel()

		if err := <-waitErr; !errors.Is(err, context.Canceled) {
			t.Errorf("wait() = %v, want context.Canceled", err)
		}
	})
}

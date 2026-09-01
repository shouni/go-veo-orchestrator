package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"
)

// countingImageGenerator counts how many times the underlying API would actually be called.
type countingImageGenerator struct {
	mu     sync.Mutex
	calls  int
	block  chan struct{}
	err    error
	cancel context.Context //nolint:containedctx // records the ctx the decorator passed down
}

func (g *countingImageGenerator) Generate(ctx context.Context, _ imagegen.Request) (*imagegen.Response, error) {
	g.mu.Lock()
	g.calls++
	g.cancel = ctx
	block := g.block
	err := g.err
	g.mu.Unlock()

	if block != nil {
		<-block
	}
	if err != nil {
		return nil, err
	}
	return &imagegen.Response{Data: []byte("image"), MIMEType: "image/png"}, nil
}

func (g *countingImageGenerator) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func imageRequest(prompt string) imagegen.Request {
	return imagegen.Request{
		Model: "m", Prompt: prompt,
	}
}

// TestSingleflightImageGeneratorCollapsesIdenticalCalls pins the dedupe: Cloud Tasks delivers
// at-least-once and clients retry, so the same generation can arrive twice at once. Image
// generation is billed per call, which is why identical in-flight requests share one.
func TestSingleflightImageGeneratorCollapsesIdenticalCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := &countingImageGenerator{block: make(chan struct{})}
		g := &singleflightImageGenerator{inner: inner}

		const callers = 4
		var wg sync.WaitGroup
		responses := make([]*imagegen.Response, callers)
		errs := make([]error, callers)
		for i := range callers {
			wg.Go(func() {
				responses[i], errs[i] = g.Generate(context.Background(), imageRequest("same"))
			})
		}

		// 全員が in-flight に入るのを待ってから解放する。Wait はバブル内の他の
		// ゴルーチンがすべて継続的にブロックした時点で返ります。
		synctest.Wait()
		close(inner.block)
		wg.Wait()

		if got := inner.callCount(); got != 1 {
			t.Errorf("underlying calls = %d, want 1 (identical requests must share one)", got)
		}
		for i := range callers {
			if errs[i] != nil {
				t.Fatalf("caller %d: err = %v", i, errs[i])
			}
			if string(responses[i].Data) != "image" {
				t.Errorf("caller %d got %q", i, responses[i].Data)
			}
		}
	})
}

// TestSingleflightImageGeneratorSeparatesDifferentRequests pins that the dedupe key covers the
// request content — collapsing two different prompts into one image would be far worse than
// paying twice.
func TestSingleflightImageGeneratorSeparatesDifferentRequests(t *testing.T) {
	inner := &countingImageGenerator{}
	g := &singleflightImageGenerator{inner: inner}

	if _, err := g.Generate(context.Background(), imageRequest("first")); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if _, err := g.Generate(context.Background(), imageRequest("second")); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Errorf("underlying calls = %d, want 2 (different prompts must not share)", got)
	}
}

// TestSingleflightImageGeneratorClonesResponse pins that each caller gets its own bytes.
// Sharing one slice would let a caller that mutates or reslices the image corrupt what the
// piggybacking callers received.
func TestSingleflightImageGeneratorClonesResponse(t *testing.T) {
	inner := &countingImageGenerator{}
	g := &singleflightImageGenerator{inner: inner}
	ctx := context.Background()

	first, err := g.Generate(ctx, imageRequest("same"))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	first.Data[0] = 'X'

	second, err := g.Generate(ctx, imageRequest("same"))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if string(second.Data) != "image" {
		t.Errorf("second caller saw %q, want the first caller's mutation not to leak", second.Data)
	}
}

// TestSingleflightImageGeneratorDetachesSharedExecution pins that one caller's cancel does not
// kill the shared run. Without the detach, whichever caller gave up first would abort the
// generation every piggybacking caller was waiting on.
func TestSingleflightImageGeneratorDetachesSharedExecution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := &countingImageGenerator{block: make(chan struct{})}
		g := &singleflightImageGenerator{inner: inner}

		leaderCtx, cancelLeader := context.WithCancel(context.Background())
		leaderDone := make(chan error, 1)
		go func() {
			_, err := g.Generate(leaderCtx, imageRequest("same"))
			leaderDone <- err
		}()

		synctest.Wait() // 先行の呼び出しが in-flight に入るのを待つ

		followerDone := make(chan error, 1)
		go func() {
			_, err := g.Generate(context.Background(), imageRequest("same"))
			followerDone <- err
		}()

		synctest.Wait() // 後続が相乗りするのを待つ
		cancelLeader()  // 先行だけを打ち切る

		if err := <-leaderDone; !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled caller err = %v, want context.Canceled", err)
		}

		close(inner.block)
		if err := <-followerDone; err != nil {
			t.Errorf("piggybacking caller err = %v, want it to survive the other caller's cancel", err)
		}
	})
}

// countingTextGenerator counts text-generation calls.
type countingTextGenerator struct {
	mu    sync.Mutex
	calls int
}

func (g *countingTextGenerator) Generate(
	_ context.Context, _, _ string, _ []gemini.Attachment, _ gemini.GenerateOptions,
) (*gemini.Response, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	return &gemini.Response{Text: "ok"}, nil
}

func (g *countingTextGenerator) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// TestGuardCoversTextGenerationToo pins the reason these guards live in workflow instead of
// being delegated to the image kit's WithRateLimit: Gemini quota is per project, not per
// operation kind, so script generation has to sit behind the same limiter as image generation.
// Guarding only the image path would leave text free to exhaust the same quota.
func TestGuardCoversTextGenerationToo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval = 30 * time.Millisecond
		guard := callguard.New(callguard.WithRateInterval(interval))

		text := &countingTextGenerator{}
		image := &countingImageGenerator{}
		textGen := &singleflightGenerator{inner: text, guard: guard}
		imageGen := &singleflightImageGenerator{inner: image, guard: guard}

		ctx := context.Background()
		start := time.Now()

		if _, err := textGen.Generate(ctx, "m", "script", nil, gemini.GenerateOptions{}); err != nil {
			t.Fatalf("text generation error = %v", err)
		}
		if _, err := imageGen.Generate(ctx, imageRequest("keyframe")); err != nil {
			t.Fatalf("image generation error = %v", err)
		}

		// 別種の操作でも同じリミッターを共有するので、2回目は間隔ぶん待たされる。
		// 仮想時計なので経過時間はちょうど interval に一致する。
		if elapsed := time.Since(start); elapsed != interval {
			t.Errorf("text then image took %v, want exactly %v (one shared limiter)", elapsed, interval)
		}
		if text.callCount() != 1 || image.callCount() != 1 {
			t.Errorf("calls = text:%d image:%d, want 1 each", text.callCount(), image.callCount())
		}
	})
}

// TestSingleflightSurfacesInnerError pins that a failure is not swallowed by the decorator.
func TestSingleflightSurfacesInnerError(t *testing.T) {
	boom := errors.New("quota exceeded")
	g := &singleflightImageGenerator{inner: &countingImageGenerator{err: boom}}

	if _, err := g.Generate(context.Background(), imageRequest("same")); !errors.Is(err, boom) {
		t.Errorf("Generate() = %v, want the inner error", err)
	}
}

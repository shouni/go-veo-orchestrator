package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shouni/go-gemini-client/gemini"
	imagePorts "github.com/shouni/vertex-image-kit/ports"
)

// countingImageGenerator counts how many times the underlying API would actually be called.
type countingImageGenerator struct {
	mu     sync.Mutex
	calls  int
	block  chan struct{}
	err    error
	cancel context.Context //nolint:containedctx // records the ctx the decorator passed down
}

func (g *countingImageGenerator) Generate(ctx context.Context, _ imagePorts.ImageRequest) (*imagePorts.ImageResponse, error) {
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
	return &imagePorts.ImageResponse{Data: []byte("image"), MimeType: "image/png"}, nil
}

func (g *countingImageGenerator) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func imageRequest(prompt string) imagePorts.ImageRequest {
	return imagePorts.ImageRequest{
		GenerationOptions: imagePorts.GenerationOptions{Model: "m", Prompt: prompt},
	}
}

// TestSingleflightImageGeneratorCollapsesIdenticalCalls pins the dedupe: Cloud Tasks delivers
// at-least-once and clients retry, so the same generation can arrive twice at once. Image
// generation is billed per call, which is why identical in-flight requests share one.
func TestSingleflightImageGeneratorCollapsesIdenticalCalls(t *testing.T) {
	inner := &countingImageGenerator{block: make(chan struct{})}
	g := &singleflightImageGenerator{inner: inner}

	const callers = 4
	var wg sync.WaitGroup
	responses := make([]*imagePorts.ImageResponse, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses[i], errs[i] = g.Generate(context.Background(), imageRequest("same"))
		}()
	}

	// 全員が in-flight に入るのを待ってから解放する。
	time.Sleep(50 * time.Millisecond)
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
	inner := &countingImageGenerator{block: make(chan struct{})}
	g := &singleflightImageGenerator{inner: inner}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := g.Generate(leaderCtx, imageRequest("same"))
		leaderDone <- err
	}()

	time.Sleep(30 * time.Millisecond) // 先行の呼び出しが in-flight に入るのを待つ

	followerDone := make(chan error, 1)
	go func() {
		_, err := g.Generate(context.Background(), imageRequest("same"))
		followerDone <- err
	}()

	time.Sleep(30 * time.Millisecond)
	cancelLeader() // 先行だけを打ち切る

	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled caller err = %v, want context.Canceled", err)
	}

	close(inner.block)
	if err := <-followerDone; err != nil {
		t.Errorf("piggybacking caller err = %v, want it to survive the other caller's cancel", err)
	}
}

// TestCallGuardAppliesTimeoutToExecution pins that RequestTimeout bounds one call.
func TestCallGuardAppliesTimeoutToExecution(t *testing.T) {
	inner := &countingImageGenerator{block: make(chan struct{})}
	g := &singleflightImageGenerator{
		inner: inner,
		guard: callGuard{timeout: 20 * time.Millisecond},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// stub は ctx を見ないので、ここでは渡された ctx が期限切れになることを確認する。
		time.Sleep(60 * time.Millisecond)
		inner.mu.Lock()
		ctx := inner.cancel
		inner.mu.Unlock()
		if ctx == nil {
			t.Error("inner generator was never called")
			return
		}
		if err := ctx.Err(); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("execution ctx err = %v, want DeadlineExceeded", err)
		}
		close(inner.block)
	}()

	if _, err := g.Generate(context.Background(), imageRequest("same")); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	<-done
}

// TestCallGuardWaitsForRateOutsideTimeout pins the ordering that keeps a busy queue from
// manufacturing timeouts: the rate-limit wait happens before the per-call deadline starts.
// If the wait were inside, a short RequestTimeout plus a long RateInterval would fail every
// call after the first, and the error would point at the API instead of at the settings.
func TestCallGuardWaitsForRateOutsideTimeout(t *testing.T) {
	const interval = 40 * time.Millisecond
	inner := &countingImageGenerator{}
	g := &singleflightImageGenerator{
		inner: inner,
		guard: callGuard{
			limiter: newRateLimiter(interval),
			timeout: 20 * time.Millisecond, // 発射間隔より短い上限
		},
	}
	ctx := context.Background()

	if _, err := g.Generate(ctx, imageRequest("first")); err != nil {
		t.Fatalf("first Generate() error = %v", err)
	}
	// 2回目は interval 待たされる。待機が上限の内側なら、ここで DeadlineExceeded になる。
	if _, err := g.Generate(ctx, imageRequest("second")); err != nil {
		t.Fatalf("second Generate() error = %v, want the rate wait to sit outside the timeout", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Errorf("underlying calls = %d, want 2", got)
	}
}

// countingTextGenerator counts text-generation calls.
type countingTextGenerator struct {
	mu    sync.Mutex
	calls int
}

func (g *countingTextGenerator) GenerateWithAttachments(
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
// being delegated to vertex-image-kit's WithRateLimit: Gemini quota is per project, not per
// operation kind, so script generation has to sit behind the same limiter as image generation.
// Guarding only the image path would leave text free to exhaust the same quota.
func TestGuardCoversTextGenerationToo(t *testing.T) {
	const interval = 30 * time.Millisecond
	limiter := newRateLimiter(interval)
	guard := callGuard{limiter: limiter}

	text := &countingTextGenerator{}
	image := &countingImageGenerator{}
	textGen := &singleflightGenerator{inner: text, guard: guard}
	imageGen := &singleflightImageGenerator{inner: image, guard: guard}

	ctx := context.Background()
	start := time.Now()

	if _, err := textGen.GenerateWithAttachments(ctx, "m", "script", nil, gemini.GenerateOptions{}); err != nil {
		t.Fatalf("text generation error = %v", err)
	}
	if _, err := imageGen.Generate(ctx, imageRequest("keyframe")); err != nil {
		t.Fatalf("image generation error = %v", err)
	}

	// 別種の操作でも同じリミッターを共有するので、2回目は間隔ぶん待たされる。
	if elapsed := time.Since(start); elapsed < interval {
		t.Errorf("text then image took %v, want at least %v (one shared limiter)", elapsed, interval)
	}
	if text.callCount() != 1 || image.callCount() != 1 {
		t.Errorf("calls = text:%d image:%d, want 1 each", text.callCount(), image.callCount())
	}
}

// TestSingleflightSurfacesInnerError pins that a failure is not swallowed by the decorator.
func TestSingleflightSurfacesInnerError(t *testing.T) {
	boom := errors.New("quota exceeded")
	g := &singleflightImageGenerator{inner: &countingImageGenerator{err: boom}}

	if _, err := g.Generate(context.Background(), imageRequest("same")); !errors.Is(err, boom) {
		t.Errorf("Generate() = %v, want the inner error", err)
	}
}

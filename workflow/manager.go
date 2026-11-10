// Package workflow provides the DI layer that assembles go-veo-orchestrator's
// Runners (script, keyframe, video, publish) from configuration and clients. The
// call interval, per-call time limit and deduplication (singleflight) of AI calls
// are applied here too (see callGuard).
package workflow

import (
	"fmt"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-veo-orchestrator/ports"
)

// PromptDeps bundles the prompt-related dependencies.
type PromptDeps struct {
	Characters     *characterkit.Characters
	ScriptPrompt   ports.ScriptPrompt
	KeyframePrompt ports.KeyframePrompt
}

// ManagerArgs is the set of arguments needed to initialize and manage the workflows.
type ManagerArgs struct {
	Config ports.Config
	// Reader fetches the script source (URL or text). Reference images reach
	// Vertex AI as gs:// URIs, unchanged, so it is not used to resolve images.
	Reader ports.ContentReader
	// Writer is where outputs and metadata are saved.
	//
	// It must be safe for concurrent use. Each cut's goroutine saves its
	// keyframe right after generating it, so Write is called concurrently
	// whenever Config.MaxConcurrency is 2 or more.
	Writer remoteio.Writer
	// AIClient is used for both the script's text generation and the keyframes'
	// image generation. Only the one generation method is required.
	AIClient    gemini.Generator
	VideoRunner ports.VideoRunner
	PromptDeps  *PromptDeps
}

// manager builds and manages the Runners that carry out each stage of the workflows.
type manager struct {
	cfg            ports.Config
	reader         ports.ContentReader
	writer         remoteio.Writer
	aiClient       gemini.Generator
	videoRunner    ports.VideoRunner
	imageGenerator imagegen.Generator
	promptDeps     *PromptDeps
}

// New initializes a new Workflows from the configuration and character definitions.
func New(args ManagerArgs) (*ports.Workflows, error) {
	if err := validateArgs(&args); err != nil {
		return nil, err
	}

	cfg := args.Config
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Text and image generation share one guard (see the header of singleflight.go for why).
	guard := callguard.New(
		callguard.WithRateInterval(cfg.RateInterval),
		callguard.WithExecTimeout(cfg.RequestTimeout),
	)

	m := &manager{
		cfg:         cfg,
		reader:      args.Reader,
		writer:      args.Writer,
		aiClient:    &singleflightGenerator{inner: args.AIClient, guard: guard},
		videoRunner: args.VideoRunner,
		promptDeps:  args.PromptDeps,
	}

	imageGenerator, err := buildImageGenerator(args.AIClient, guard)
	if err != nil {
		return nil, err
	}
	m.imageGenerator = imageGenerator

	return m.buildAllRunners()
}

// validateArgs validates the arguments.
//
// The messages use ManagerArgs' actual field names. Callers rely on the message
// to find what to fix, so if the names drift, grepping for them finds nothing.
func validateArgs(args *ManagerArgs) error {
	if args.Reader == nil {
		return fmt.Errorf("ManagerArgs.Reader is required")
	}
	if args.Writer == nil {
		return fmt.Errorf("ManagerArgs.Writer is required")
	}
	if args.AIClient == nil {
		return fmt.Errorf("ManagerArgs.AIClient is required")
	}
	if args.PromptDeps == nil {
		return fmt.Errorf("ManagerArgs.PromptDeps is required")
	}
	if args.PromptDeps.Characters == nil {
		return fmt.Errorf("ManagerArgs.PromptDeps.Characters is required")
	}
	if args.PromptDeps.ScriptPrompt == nil {
		return fmt.Errorf("ManagerArgs.PromptDeps.ScriptPrompt is required")
	}
	if args.PromptDeps.KeyframePrompt == nil {
		return fmt.Errorf("ManagerArgs.PromptDeps.KeyframePrompt is required")
	}

	return nil
}

// buildImageGenerator assembles the image generation engine.
//
// imagegen only hands reference images to Vertex AI as gs:// URIs, unchanged, so
// nothing is fetched, uploaded or cached. It is given just the one generation
// client, and passing no options is intended (the call interval and time limit are
// applied on the callGuard side). If http(s) references ever become necessary,
// that is the signal to go back to gemini-image-kit.
func buildImageGenerator(client gemini.Generator, guard *callguard.Guard) (imagegen.Generator, error) {
	gen, err := imagegen.New(client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize the image generator: %w", err)
	}
	return &singleflightImageGenerator{inner: gen, guard: guard}, nil
}

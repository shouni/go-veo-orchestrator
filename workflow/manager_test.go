package workflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/shouni/genai-kit/gemini"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

func TestNewBuildsWorkflows(t *testing.T) {
	workflows, err := New(testManagerArgs())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if workflows.Script == nil {
		t.Fatal("Script runner is nil")
	}
	if workflows.CutKeyframe == nil {
		t.Fatal("CutKeyframe runner is nil")
	}
	if workflows.Publish == nil {
		t.Fatal("Publish runner is nil")
	}
	if workflows.Video == nil {
		t.Fatal("Video runner should not be nil even without a VideoRunner dependency")
	}
	if _, err := workflows.Video.Run(context.Background(), &video.Recipe{}); !errors.Is(err, ports.ErrVideoRunnerNotConfigured) {
		t.Fatalf("expected ErrVideoRunnerNotConfigured when no VideoRunner is configured, got %v", err)
	}
}

func TestNewRejectsMissingRequiredConfig(t *testing.T) {
	cases := map[string]func(*ports.Config){
		"GeminiModel": func(c *ports.Config) { c.GeminiModel = "" },
		"ImageModel":  func(c *ports.Config) { c.ImageModel = "  " },
		// The kit holds no art-direction defaults, so ratio and resolution are
		// required too. Sent empty, the picture is whatever the model picks, and the
		// orientation or resolution changes without anyone noticing.
		"KeyframeAspectRatio": func(c *ports.Config) { c.KeyframeAspectRatio = "" },
		"KeyframeImageSize":   func(c *ports.Config) { c.KeyframeImageSize = " " },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			args := testManagerArgs()
			mutate(&args.Config)
			_, err := New(args)
			if !errors.Is(err, ports.ErrConfigInvalid) {
				t.Errorf("New() without %s: err = %v, want ErrConfigInvalid", name, err)
			}
		})
	}
}

// The Close test was removed once the image cache was gone. The image cache and
// its periodic cleanup goroutine no longer exist, and Workflows holds no resources
// left to release (Close itself was removed too).

func testManagerArgs() ManagerArgs {
	chars, err := newTestCharacters([]characterkit.Character{
		{ID: "main", Name: "Main", VisualCues: []string{"blue jacket"}, ReferenceURL: "gs://bucket/main.png", IsDefault: true},
	})
	if err != nil {
		panic(err)
	}

	return ManagerArgs{
		Config: ports.Config{
			GeminiModel:         "gemini-text",
			ImageModel:          "gemini-image",
			KeyframeAspectRatio: "16:9",
			KeyframeImageSize:   "2K",
		},
		Reader:   fakeContentReader{},
		Writer:   fakeWriter{},
		AIClient: fakeGenerativeModel{},
		PromptDeps: &PromptDeps{
			Characters:     chars,
			ScriptPrompt:   fakeScriptPrompt{},
			KeyframePrompt: fakeKeyframePrompt{},
		},
	}
}

func newTestCharacters(list []characterkit.Character) (*characterkit.Characters, error) {
	return characterkit.NewCharacters(list)
}

// fakeGenerativeModel implements only gemini.Generator (one method).
//
// It used to implement File API management (UploadFile / DeleteFile) and backend
// detection too, because ManagerArgs.AIClient required a gemini.Model. The kit no
// longer uses the File API and gemini.Generator is enough, so the unused methods
// could go. It does not implement BackendInspector, so the image kit's backend
// detection (an optional interface) passes it by.
type fakeGenerativeModel struct{}

func (fakeGenerativeModel) Generate(context.Context, string, string, []gemini.Attachment, gemini.GenerateOptions) (*gemini.Response, error) {
	return &gemini.Response{}, nil
}

type fakeContentReader struct{}

func (fakeContentReader) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(http.NoBody), nil
}

type fakeWriter struct{}

func (fakeWriter) Write(context.Context, string, io.Reader, ...remoteio.WriteOption) error {
	return nil
}

func (fakeWriter) Delete(context.Context, string) error {
	return nil
}

type fakeScriptPrompt struct{}

func (fakeScriptPrompt) Build(string, *ports.TemplateData) (string, error) {
	return "prompt", nil
}

type fakeKeyframePrompt struct{}

func (fakeKeyframePrompt) BuildCut(video.Cut, *characterkit.Character) (string, string) {
	return "user", "system"
}

func (fakeKeyframePrompt) BuildEdit(video.Cut, *characterkit.Character, string) (string, string) {
	return "edit-user", "edit-system"
}

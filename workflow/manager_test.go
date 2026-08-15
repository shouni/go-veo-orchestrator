package workflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-veo-orchestrator/ports"
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
	if _, err := workflows.Video.Run(context.Background(), &ports.VideoRecipe{}); !errors.Is(err, ports.ErrVideoRunnerNotConfigured) {
		t.Fatalf("expected ErrVideoRunnerNotConfigured when no VideoRunner is configured, got %v", err)
	}
}

func TestNewRejectsMissingModels(t *testing.T) {
	cases := map[string]func(*ports.Config){
		"GeminiModel": func(c *ports.Config) { c.GeminiModel = "" },
		"ImageModel":  func(c *ports.Config) { c.ImageModel = "  " },
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

// Close のテストは vertex-image-kit への移行で削除しました。画像キャッシュと
// その定期クリーンアップ goroutine が無くなり、Workflows に解放すべき資源が
// 残っていないためです（Close 自体も削除しました）。

func testManagerArgs() ManagerArgs {
	chars, err := newTestCharacters([]characterkit.Character{
		{ID: "main", Name: "Main", VisualCues: []string{"blue jacket"}, ReferenceURL: "gs://bucket/main.png", IsDefault: true},
	})
	if err != nil {
		panic(err)
	}

	return ManagerArgs{
		Config: ports.Config{
			GeminiModel: "gemini-text",
			ImageModel:  "gemini-image",
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

// fakeGenerativeModel は gemini.Generator（1 メソッド）だけを実装します。
//
// 以前は File API 管理（UploadFile / DeleteFile）とバックエンド判定も実装していました。
// ManagerArgs.AIClient が gemini.Model を要求していたためですが、キットが File API を
// 使わなくなり gemini.Generator で足りるようになったので、使わないメソッドは消せます。
// BackendInspector を実装しないため、vertex-image-kit のバックエンド判定
// （オプショナルインターフェース）は素通りします。
type fakeGenerativeModel struct{}

func (fakeGenerativeModel) GenerateWithAttachments(context.Context, string, string, []gemini.Attachment, gemini.GenerateOptions) (*gemini.Response, error) {
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

func (fakeKeyframePrompt) BuildCut(ports.Cut, *characterkit.Character) (string, string) {
	return "user", "system"
}

func (fakeKeyframePrompt) BuildEdit(ports.Cut, *characterkit.Character, string) (string, string) {
	return "edit-user", "edit-system"
}

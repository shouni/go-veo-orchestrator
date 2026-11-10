package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"

	"github.com/shouni/genai-kit/gemini"
	characterkit "github.com/shouni/go-character-kit/character"
	"github.com/shouni/go-veo-orchestrator/ports"
	"github.com/shouni/go-veo-orchestrator/video"
)

const (
	// maxInputSize is the largest text size allowed to be read (5MB).
	maxInputSize int64 = 5 * 1024 * 1024
	// maxErrorResponseLength is the maximum number of characters of the response excerpt included in error logs.
	maxErrorResponseLength = 200
)

// jsonBlockRegex is the regular expression for extracting Markdown JSON blocks.
// Its non-greedy match extracts each block separately even when the response has
// several code blocks.
var jsonBlockRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?\\S)\\s*```")

// VideoScriptRunner reads a Music Recipe from an input source and generates a video recipe.
type VideoScriptRunner struct {
	promptBuilder ports.ScriptPrompt
	aiClient      gemini.Generator
	reader        ports.ContentReader
	aiModel       string
	characters    *characterkit.Characters
}

// NewVideoScriptRunner initializes the runner with its dependencies injected.
// characters is used to constrain the AI-generated cuts[].character_id to known
// character IDs (an enum in the ResponseSchema). When nil, there is no constraint
// (only the empty string is allowed).
func NewVideoScriptRunner(
	pb ports.ScriptPrompt,
	ai gemini.Generator,
	r ports.ContentReader,
	aiModel string,
	characters *characterkit.Characters,
) *VideoScriptRunner {
	return &VideoScriptRunner{
		promptBuilder: pb,
		aiClient:      ai,
		reader:        r,
		aiModel:       aiModel,
		characters:    characters,
	}
}

// characterIDs returns the list of character IDs used for the schema's character_id enum.
func (r *VideoScriptRunner) characterIDs() []string {
	if r.characters.Len() == 0 {
		return nil
	}
	ids := make([]string, 0, r.characters.Len())
	for _, c := range r.characters.All() {
		ids = append(ids, c.ID)
	}
	return ids
}

// Run reads Music Recipe JSON and generates video script JSON with Gemini.
func (r *VideoScriptRunner) Run(ctx context.Context, sourceURL string, mode string) (*video.Recipe, error) {
	slog.InfoContext(ctx, "ScriptRunner: starting", "url", sanitizeURL(sourceURL))

	// 1. Fetch the text from the source
	inputText, err := r.readContent(ctx, sourceURL)
	if err != nil {
		return nil, err
	}

	// 2. Build the prompt
	sourceRecipe, err := parseSourceRecipe(inputText)
	if err != nil {
		return nil, err
	}
	if sourceRecipe == nil {
		return nil, fmt.Errorf("could not parse the content of sourceURL as Music Recipe JSON")
	}
	data := ports.TemplateData{
		SourceRecipe: sourceRecipe,
	}
	finalPrompt, err := r.promptBuilder.Build(mode, &data)
	if err != nil {
		return nil, fmt.Errorf("failed to build the prompt: %w", err)
	}

	// 3. Call the Gemini API, regenerating until a usable script comes back
	return r.generateRecipe(ctx, finalPrompt, sourceRecipe)
}

// maxScriptAttempts is the maximum number of times a script that fails
// validation is regenerated.
//
// Giving up after one attempt turns generation noise straight into a job failure,
// and persisting without limit just piles up script generation charges. If a few
// tries do not fix it, the prompt or schema is what is broken.
const maxScriptAttempts = 3

// generateRecipe generates the script and retries until it has a structure the
// downstream steps can handle.
//
// A JSON Schema constrains only types and required fields, so breakage such as an
// empty cuts list or a section_index beyond the number of song sections slips
// through. Not rejecting it here means failing only after reaching Veo cut
// generation, the most expensive step in the pipeline.
func (r *VideoScriptRunner) generateRecipe(ctx context.Context, finalPrompt string, sourceRecipe *video.Recipe) (*video.Recipe, error) {
	opts := gemini.GenerateOptions{
		ResponseMIMEType:   "application/json",
		ResponseJSONSchema: video.RecipeSchema(r.characterIDs()),
	}

	var lastErr error
	for attempt := 1; attempt <= maxScriptAttempts; attempt++ {
		slog.InfoContext(ctx, "ScriptRunner: calling the Gemini API", "model", r.aiModel, "attempt", attempt)

		resp, err := r.aiClient.Generate(ctx, r.aiModel, finalPrompt, nil, opts)
		if err != nil {
			return nil, fmt.Errorf("content generation with gemini failed: %w", err)
		}
		if resp == nil || strings.TrimSpace(resp.Text) == "" {
			return nil, fmt.Errorf("AI client returned an empty response: %w", ports.ErrInvalidAIResponse)
		}
		if resp.Usage != nil {
			slog.InfoContext(ctx, "ScriptRunner: token usage",
				"prompt_tokens", resp.Usage.PromptTokenCount,
				"candidates_tokens", resp.Usage.CandidatesTokenCount,
				"total_tokens", resp.Usage.TotalTokenCount,
			)
		}

		recipe, err := r.parseResponse(ctx, resp.Text)
		if err != nil {
			// A response that cannot be read as JSON is unlikely to be fixed by regenerating, so return it as-is.
			return nil, err
		}

		// music_recipe is deliberately excluded from the schema (the AI does not
		// generate it), so carry over the source's value as-is. section_index is
		// validated only after this is in place.
		recipe.MusicRecipe = sourceRecipe.MusicRecipe
		recipe.Normalize()

		lastErr = recipe.Validate()
		if lastErr == nil {
			return recipe, nil
		}
		slog.WarnContext(ctx, "ScriptRunner: generated script is invalid, regenerating",
			"attempt", attempt, "max_attempts", maxScriptAttempts, "err", lastErr)
	}

	return nil, fmt.Errorf("generated script was invalid on all %d attempts: %w", maxScriptAttempts, lastErr)
}

// readContent fetches content from the given source URL.
func (r *VideoScriptRunner) readContent(ctx context.Context, sourceURL string) (string, error) {
	rc, err := r.reader.Open(ctx, sourceURL)
	if err != nil {
		return "", fmt.Errorf("failed to read source: %w", err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			slog.WarnContext(ctx, "Failed to close the stream", "error", closeErr)
		}
	}()
	// Read one byte more than the limit and judge overflow by whether anything
	// is left over. Reading exactly up to the limit and then peeking one byte with
	// rc.Read would make the check depend, for that alone, on how the underlying
	// stream returns EOF (io.EOF itself or a wrapped EOF).
	content, err := io.ReadAll(io.LimitReader(rc, maxInputSize+1))
	if err != nil {
		return "", fmt.Errorf("failed to read: %w", err)
	}
	if int64(len(content)) > maxInputSize {
		return "", fmt.Errorf("input exceeds the limit of %d bytes (url: %s): %w",
			maxInputSize, sanitizeURL(sourceURL), ports.ErrInputTooLarge)
	}

	return strings.ToValidUTF8(string(content), ""), nil
}

// parseResponse extracts JSON candidates from the AI response and adopts the first
// one with video recipe content. It also handles responses that mix in JSON blocks
// used for explanation.
func (r *VideoScriptRunner) parseResponse(ctx context.Context, raw string) (*video.Recipe, error) {
	candidates := extractJSONCandidates(raw)
	if len(candidates) == 0 {
		slog.WarnContext(ctx, "Could not extract JSON from the AI response; trying to parse the whole response",
			"response_snippet", truncateString(raw, 100))
		candidates = []string{raw}
	}

	var parseErr error
	for _, jsonStr := range candidates {
		var recipe video.Recipe
		if err := json.Unmarshal([]byte(jsonStr), &recipe); err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		if !hasVideoRecipeContent(&recipe) {
			continue
		}
		recipe.Normalize()
		return &recipe, nil
	}

	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse AI response JSON (excerpt: %q): %w: %w",
			truncateString(raw, maxErrorResponseLength), ports.ErrInvalidAIResponse, parseErr)
	}
	return nil, fmt.Errorf("AI response JSON contains no video recipe content (excerpt: %q): %w",
		truncateString(raw, maxErrorResponseLength), ports.ErrInvalidAIResponse)
}

func parseSourceRecipe(raw string) (*video.Recipe, error) {
	var parseErr error
	for _, jsonStr := range extractJSONCandidates(raw) {
		var recipe video.Recipe
		if err := json.Unmarshal([]byte(jsonStr), &recipe); err == nil && hasVideoRecipeContent(&recipe) {
			recipe.Normalize()
			return &recipe, nil
		}

		var musicRecipe video.MusicRecipe
		if err := json.Unmarshal([]byte(jsonStr), &musicRecipe); err != nil {
			if parseErr == nil {
				parseErr = err
			}
			continue
		}
		if !hasMusicRecipeContent(&musicRecipe) {
			continue
		}

		newRecipe := video.Recipe{
			MusicRecipe: musicRecipe,
		}
		newRecipe.Normalize()
		return &newRecipe, nil
	}

	if parseErr != nil {
		return nil, fmt.Errorf("failed to parse source JSON: %w", parseErr)
	}
	return nil, nil
}

func hasVideoRecipeContent(recipe *video.Recipe) bool {
	return recipe.ProjectTitle != "" ||
		recipe.Description != "" ||
		len(recipe.Cuts) > 0 ||
		hasMusicRecipeContent(&recipe.MusicRecipe)
}

func hasMusicRecipeContent(recipe *video.MusicRecipe) bool {
	return recipe.Title != "" ||
		recipe.Theme != "" ||
		recipe.Mood != "" ||
		recipe.Tempo != 0 ||
		len(recipe.Instruments) > 0 ||
		len(recipe.Sections) > 0 ||
		recipe.Lyrics != nil ||
		recipe.AudioModel != "" ||
		recipe.ComposeMode != "" ||
		recipe.Seed != nil
}

// extractJSONCandidates returns, in priority order, the candidates in a string
// that could be interpreted as JSON. It lists Markdown code blocks as separate
// candidates and adds the JSON outside the fences last.
//
// There are several candidates because some responses mix in JSON blocks used for
// explanation. The caller takes "the first candidate with recipe content", so it
// is not cut short by a leading explanatory block.
//
// Extraction and repair themselves are left to gemini.CleanJSONResponse. This used
// to cut "from the first { to the last }" by hand, but when prose followed a
// complete JSON value it picked up the wrong last } and shifted the range, and it
// could not fix raw newlines or bare backslashes inside strings. Recipes carrying
// lyrics and dialogue hit that in production. CleanJSONResponse leaves input that
// already parses unchanged, so running fenced contents through it passes them
// through untouched.
func extractJSONCandidates(raw string) []string {
	cleanRaw := strings.TrimSpace(raw)

	var candidates []string
	for _, matches := range jsonBlockRegex.FindAllStringSubmatch(cleanRaw, -1) {
		candidates = append(candidates, gemini.CleanJSONResponse(matches[1]))
	}

	// Return no candidates for a response without a JSON start character. The
	// caller detects that, records "could not extract", and switches to parsing
	// the whole response.
	if strings.ContainsAny(cleanRaw, "{[") {
		candidates = append(candidates, gemini.CleanJSONResponse(cleanRaw))
	}

	return candidates
}

// sanitizeURL strips the query parameters and fragment from a URL for output to
// logs and error messages, keeping the credentials of signed URLs from leaking.
func sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparsable url)"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// truncateString safely truncates a string to the given length.
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

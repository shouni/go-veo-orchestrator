package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/imagegen"
)

// This file holds the decorators that put call guards over the script's text
// generation and the keyframes' image generation, and the code that builds keys
// from request contents. The call interval, time limit and deduplication of
// concurrent calls themselves belong to callguard, which shares its
// implementation with go-comic-kit and genai-kit/lyria.
//
// One guard (callguard.Guard) is shared across the whole workflow and applied to
// both text and image generation. Quota is per project, not per kind of
// operation, so throttling only one of them is pointless. That is also why the
// call interval and time limit live here rather than in image kit options
// (WithRateLimit / WithRequestTimeout).

// singleflightImageGenerator is a decorator that collapses concurrent image
// generation requests with identical contents into one call. It protects the
// expensive image generation API from duplicate calls caused by Cloud Tasks'
// at-least-once delivery and retries. It covers in-flight calls within the process
// only; durable deduplication comes from job-side idempotency through the recipe's
// keyframe_reference.
type singleflightImageGenerator struct {
	inner imagegen.Generator
	guard *callguard.Guard
	group callguard.Group
}

var _ imagegen.Generator = (*singleflightImageGenerator)(nil)

// Generate collapses concurrent calls, keyed by a hash of the request contents.
func (g *singleflightImageGenerator) Generate(ctx context.Context, req imagegen.Request) (*imagegen.Response, error) {
	key := imageRequestKey(&req)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*imagegen.Response, error) {
		return g.inner.Generate(execCtx, req)
	})
	if err != nil {
		return nil, err
	}
	return cloneImageResponse(resp), nil
}

// singleflightGenerator is a gemini.Generator decorator that collapses concurrent
// text generation requests with identical contents into one call.
type singleflightGenerator struct {
	inner gemini.Generator
	guard *callguard.Guard
	group callguard.Group
}

var _ gemini.Generator = (*singleflightGenerator)(nil)

// Generate collapses concurrent calls, keyed by a hash of the request contents.
func (g *singleflightGenerator) Generate(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	key := textRequestKey(modelName, prompt, attachments, &opts)
	resp, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*gemini.Response, error) {
		return g.inner.Generate(execCtx, modelName, prompt, attachments, opts)
	})
	if err != nil {
		return nil, err
	}
	// NOTE: returns a shallow copy, on the assumption that the caller (runner)
	// reads only Text. If uses that rewrite gemini.Response's reference-typed
	// fields appear, switch to a deep copy.
	cloned := *resp
	return &cloned, nil
}

// imageRequestKey builds the singleflight key from the contents of an image
// generation request.
//
// go-comic-kit's function of the same name also puts File API URIs in the key, but
// here the reference is a single gs:// URI string (imagegen neither fetches nor
// uploads).
func imageRequestKey(req *imagegen.Request) string {
	parts := []string{
		req.Model,
		req.Prompt,
		req.SystemPrompt,
		req.NegativePrompt,
		req.AspectRatio,
		req.ImageSize,
		callguard.SeedKey(req.Seed),
	}
	parts = append(parts, req.Images...)
	return callguard.Key("image", parts...)
}

// textRequestKey builds the singleflight key from the contents of a text
// generation request.
//
// An attachment is either a URI or bytes, so the key includes the URI as-is, or a
// hash of the bytes' contents. Substituting just the length would give two
// different images of the same size the same key.
//
// Of GenerateOptions, only ResponseMIMEType and Seed go into the key.
// ResponseJSONSchema (RecipeSchema(characterIDs()) in script.go) can be left out
// because the character roster is an embedded asset read once at startup and does
// not change within the process. If the roster ever becomes per-job, add it to the
// key. Otherwise, concurrent calls with the same prompt but different character_id
// enums would share one of their results.
func textRequestKey(modelName string, prompt string, attachments []gemini.Attachment, opts *gemini.GenerateOptions) string {
	keyParts := []string{modelName, opts.ResponseMIMEType, callguard.SeedKey(opts.Seed), prompt}
	for _, attachment := range attachments {
		keyParts = append(keyParts, attachment.MIMEType, attachment.URI)
		if len(attachment.Data) > 0 {
			sum := sha256.Sum256(attachment.Data)
			keyParts = append(keyParts, hex.EncodeToString(sum[:]))
		}
	}
	return callguard.Key("text", keyParts...)
}

// cloneImageResponse copies a response shared through singleflight so each caller can handle it safely.
func cloneImageResponse(src *imagegen.Response) *imagegen.Response {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Data = bytes.Clone(src.Data)
	return &dst
}

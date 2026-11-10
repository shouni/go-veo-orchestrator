package keyframe

// Option is a function type that applies a Generator setting.
//
// There are no art-direction defaults. Aspect ratio, resolution and negative prompt
// are all decided by the caller (through ports.Config) — see the Art Direction
// section of ports.Config for why. There are no options for concurrency, call
// interval or time limit here either (runner.CutKeyframeRunner and workflow's
// callGuard own those).
type Option func(*Generator)

// WithAspectRatio sets the aspect ratio of the generated keyframe images (e.g.
// "16:9", "9:16"). An empty string changes nothing. When unset, an empty value
// reaches the model, but ports.Config.KeyframeAspectRatio is required, so it is
// never empty as long as the generator is built through workflow.
func WithAspectRatio(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.aspectRatio = value
		}
	}
}

// WithImageSize sets the resolution of the generated keyframe images (e.g. "1K",
// "2K"). An empty string changes nothing.
func WithImageSize(value string) Option {
	return func(g *Generator) {
		if value != "" {
			g.imageSize = value
		}
	}
}

// WithNegativePrompt sets the negative prompt for keyframe generation. There is no
// default wording (see ports.Config.KeyframeNegativePrompt for why).
func WithNegativePrompt(value string) Option {
	return func(g *Generator) {
		g.negativePrompt = value
	}
}

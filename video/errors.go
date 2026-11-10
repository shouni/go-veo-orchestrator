package video

import "errors"

// ErrRecipeInvalid is the sentinel wrapping the structural problems Recipe.Validate
// finds (missing title, no cuts, out-of-range section_index). When validating an
// AI-generated script, it separates "a failure that regenerating might fix" from
// communication errors and the like.
//
// It lives here while the other sentinels live in ports because this package's
// Validate returns it, and the recipe package, being the bottom layer, cannot
// import ports (ports imports it).
var ErrRecipeInvalid = errors.New("video recipe is invalid")

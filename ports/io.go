package ports

import (
	"context"
	"io"
)

// ContentReader fetches content from a given URI.
type ContentReader interface {
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
}

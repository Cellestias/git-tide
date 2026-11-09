package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrStop returned by a page handler ends the walk early; it is not an error
// for the caller and the result is not reported as truncated.
var ErrStop = errors.New("stop")

// Pager walks a list endpoint that takes per_page and page parameters.
type Pager struct {
	// Get fetches one URL and returns the body, mapping statuses to errors.
	Get func(ctx context.Context, url string) ([]byte, error)
	// Label names the API in decode errors, such as "GitHub".
	Label    string
	PageSize int
	// MaxPages caps the walk; reaching it with a full last page means truncated.
	MaxPages int
}

// Each calls handle with the entries of every page, oldest page first. A page
// shorter than PageSize ends the walk.
func (p Pager) Each(ctx context.Context, endpoint string, handle func([]json.RawMessage) error) (truncated bool, err error) {
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	for page := 1; page <= p.MaxPages; page++ {
		body, err := p.Get(ctx, fmt.Sprintf("%s%sper_page=%d&page=%d", endpoint, sep, p.PageSize, page))
		if err != nil {
			return false, err
		}
		var items []json.RawMessage
		if err := json.Unmarshal(body, &items); err != nil {
			return false, fmt.Errorf("decode %s API response: %w", p.Label, err)
		}
		if err := handle(items); err != nil {
			if errors.Is(err, ErrStop) {
				return false, nil
			}
			return false, fmt.Errorf("decode %s API response: %w", p.Label, err)
		}
		if len(items) < p.PageSize {
			return false, nil
		}
	}
	return true, nil
}

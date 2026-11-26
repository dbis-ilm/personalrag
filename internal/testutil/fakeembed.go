// Package testutil holds test helpers shared across packages.
package testutil

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"

	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/core/api"
)

// FakeDim is the dimension of FakeEmbedder vectors.
const FakeDim = 64

// FakeEmbedder maps words to hashed buckets, so texts sharing words end up
// close. Deterministic and offline.
type FakeEmbedder struct{}

func (FakeEmbedder) Name() string          { return "fake" }
func (FakeEmbedder) Register(api.Registry) {}

func (FakeEmbedder) Embed(_ context.Context, req *ai.EmbedRequest) (*ai.EmbedResponse, error) {
	out := &ai.EmbedResponse{}
	for _, d := range req.Input {
		v := make([]float32, FakeDim)
		var text strings.Builder
		for _, p := range d.Content {
			text.WriteString(p.Text)
			text.WriteString(" ")
		}
		for _, w := range strings.FieldsFunc(strings.ToLower(text.String()), func(r rune) bool { return !unicode.IsLetter(r) }) {
			h := fnv.New32a()
			h.Write([]byte(w))
			v[h.Sum32()%FakeDim]++
		}
		var n float64
		for _, x := range v {
			n += float64(x * x)
		}
		if n > 0 {
			for i := range v {
				v[i] /= float32(math.Sqrt(n))
			}
		}
		out.Embeddings = append(out.Embeddings, &ai.Embedding{Embedding: v})
	}
	return out, nil
}

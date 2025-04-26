package unl

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/jasonthorsness/unlurker/hn"
	"github.com/jasonthorsness/unlurker/testdata"
)

func BenchmarkPrettyFormatDuration(b *testing.B) {
	for b.Loop() {
		for j := range 4 * 60 {
			_ = PrettyFormatDuration(time.Duration(j) * time.Minute)
		}
	}
}

func BenchmarkPrettyFormatTitle(b *testing.B) {
	items := make([]*hn.Item, 0, testdata.MaxItem-testdata.MinItem)

	for i := testdata.MaxItem; i > testdata.MinItem; i-- {
		reader, err := testdata.Getter.Get(b.Context(), "item/"+strconv.Itoa(i))
		if err != nil {
			b.Fatal(err)
		}

		var item hn.Item
		decoder := json.NewDecoder(reader)

		err = decoder.Decode(&item)
		if err != nil {
			b.Fatal(err)
		}

		items = append(items, &item)
	}

	b.Run("inner", func(b *testing.B) {
		for b.Loop() {
			for _, item := range items {
				_ = PrettyFormatTitle(item, true)
			}
		}
	})
}

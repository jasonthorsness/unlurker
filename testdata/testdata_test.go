package testdata

import (
	"io"
	"strconv"
	"testing"
)

func TestSanity(t *testing.T) {
	t.Parallel()

	g := getter{}

	data, err := g.Get(t.Context(), "item/"+strconv.Itoa(MaxItem)+".json")
	if err != nil {
		t.Fatal(err)
	}

	buf, err := io.ReadAll(data)
	if err != nil {
		t.Fatal(err)
	}

	if string(buf) == "null" {
		t.Fatal("item not found")
	}
}

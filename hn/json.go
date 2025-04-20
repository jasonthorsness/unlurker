package hn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

func (u *User) Marshal() ([]byte, error) {
	var buf bytes.Buffer

	err := u.WriteJSON(&buf)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (u *User) WriteJSON(w io.Writer) error {
	pw := startObject(w)

	writeJSONProperty(&pw, "\"about\":", u.About, isDefault[string], writeJSONString[string])
	writeJSONProperty(&pw, "\"created\":", u.Created, isDefault[int64], writeJSONInt[int64])
	writeJSONProperty(&pw, "\"id\":", u.ID, isDefault[string], writeJSONString[string])
	writeJSONProperty(&pw, "\"karma\":", u.Karma, isDefault[int], writeJSONInt[int])
	writeJSONProperty(&pw, "\"submitted\":", u.Submitted, isEmptySlice[int], writeJSONIntSlice[int])

	return pw.closeObject()
}

func (item *Item) Marshal() ([]byte, error) {
	var buf bytes.Buffer

	err := item.WriteJSON(&buf)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (item *Item) WriteJSON(w io.Writer) error {
	// This conforms to the serialization for new items (since many millions of items ago...)
	// Very old items didn't omit empty "text" and might have other differences.
	descendantsSkip := isDefault[int]

	if (item.Type == Story || item.Type == Poll) && !item.Dead && !item.Deleted {
		descendantsSkip = neverSkip[int]
	}

	pw := startObject(w)

	writeJSONProperty(&pw, "\"by\":", item.By, isDefault[string], writeJSONString[string])
	writeJSONProperty(&pw, "\"dead\":", item.Dead, isDefault[bool], writeJSONBool)
	writeJSONProperty(&pw, "\"deleted\":", item.Deleted, isDefault[bool], writeJSONBool)
	writeJSONProperty(&pw, "\"descendants\":", item.Descendants, descendantsSkip, writeJSONInt[int])
	writeJSONProperty(&pw, "\"id\":", item.ID, isDefault[int], writeJSONInt[int])
	writeJSONProperty(&pw, "\"kids\":", item.Kids, isEmptySlice[int], writeJSONIntSlice[int])
	writeJSONProperty(&pw, "\"parent\":", item.Parent, isDefault[*int], writeJSONIntP[int])
	writeJSONProperty(&pw, "\"poll\":", item.Poll, isDefault[*int], writeJSONIntP[int])
	writeJSONProperty(&pw, "\"parts\":", item.Parts, isEmptySlice[int], writeJSONIntSlice[int])
	writeJSONProperty(&pw, "\"score\":", item.Score, isDefault[int], writeJSONInt[int])
	writeJSONProperty(&pw, "\"text\":", item.Text, isDefault[string], writeJSONString[string])
	writeJSONProperty(&pw, "\"time\":", item.Time, isDefault[int64], writeJSONInt[int64])
	writeJSONProperty(&pw, "\"title\":", item.Title, isDefault[string], writeJSONString[string])
	writeJSONProperty(&pw, "\"type\":", item.Type, isDefault[ItemType], writeJSONString[ItemType])
	writeJSONProperty(&pw, "\"url\":", item.URL, isDefault[string], writeJSONString[string])

	return pw.closeObject()
}

type objectWriter struct {
	err   error
	inner io.Writer
	d     string
}

func startObject(w io.Writer) objectWriter {
	err := writeByte(w, '{')
	if err != nil {
		return objectWriter{err, nil, ""}
	}

	return objectWriter{nil, w, ""}
}

func (pw objectWriter) closeObject() error {
	if pw.err != nil {
		return pw.err
	}

	return writeByte(pw.inner, '}')
}

func writeJSONProperty[T any](
	pw *objectWriter,
	prefix string,
	v T,
	skip func(T) bool,
	write func(io.Writer, T) error,
) {
	if pw.err != nil || skip(v) {
		return
	}

	_, err := io.WriteString(pw.inner, pw.d)
	if err != nil {
		pw.err = wrapWriterError(err)
		return
	}

	pw.d = ","

	_, err = io.WriteString(pw.inner, prefix)
	if err != nil {
		pw.err = wrapWriterError(err)
		return
	}

	err = write(pw.inner, v)
	if err != nil {
		pw.err = err
		return
	}
}

func neverSkip[T comparable](_ T) bool {
	return false
}

func isDefault[T comparable](v T) bool {
	var d T
	return v == d
}

func isEmptySlice[T comparable](v []T) bool {
	return len(v) == 0
}

type noNewlineWriter struct {
	inner io.Writer
}

func (w noNewlineWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && p[len(p)-1] == '\n' {
		p = p[:len(p)-1]
	}

	return w.inner.Write(p)
}

func writeJSONString[T any](w io.Writer, v T) error {
	enc := json.NewEncoder(noNewlineWriter{w})
	enc.SetEscapeHTML(false)

	err := enc.Encode(v)
	if err != nil {
		return wrapWriterError(err)
	}

	return nil
}

func writeJSONBool(w io.Writer, v bool) error {
	if v {
		_, err := io.WriteString(w, "true")
		if err != nil {
			return wrapWriterError(err)
		}
	} else {
		_, err := io.WriteString(w, "false")
		if err != nil {
			return wrapWriterError(err)
		}
	}

	return nil
}

type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32
}

func writeJSONIntP[T Integer](w io.Writer, v *T) error {
	return writeJSONInt(w, *v)
}

func writeJSONInt[T Integer](w io.Writer, v T) error {
	const base10 = 10

	var buf [20]byte
	b := strconv.AppendInt(buf[:0], int64(v), base10)

	_, err := w.Write(b)
	if err != nil {
		return wrapWriterError(err)
	}

	return nil
}

// requires non-empty int slice.
func writeJSONIntSlice[T Integer](w io.Writer, v []T) error {
	err := writeByte(w, '[')
	if err != nil {
		return err
	}

	err = writeJSONInt(w, v[0])
	if err != nil {
		return err
	}

	delim := []byte{','}
	for _, vv := range v[1:] {
		_, err = w.Write(delim)
		if err != nil {
			return wrapWriterError(err)
		}

		err = writeJSONInt(w, vv)
		if err != nil {
			return err
		}
	}

	err = writeByte(w, ']')
	if err != nil {
		return err
	}

	return nil
}

func wrapWriterError(err error) error {
	return fmt.Errorf("failed writing JSON to writer: %w", err)
}

func writeByte(w io.Writer, b byte) error {
	_, err := w.Write([]byte{b})
	if err != nil {
		return wrapWriterError(err)
	}

	return nil
}

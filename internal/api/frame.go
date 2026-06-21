package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// WriteMessage writes v as one NDJSON frame: a single JSON object followed by a
// newline (json.Encoder appends it). It is the single shared writer so the
// daemon and CLI cannot frame differently.
func WriteMessage(w io.Writer, v any) error {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return fmt.Errorf("api: write message: %w", err)
	}
	return nil
}

// ReadMessage decodes one NDJSON frame from r into v. Malformed JSON and a
// truncated frame return a wrapped error (never a panic). For sequential frames
// off one connection use Decoder, which buffers across reads; a fresh Decoder
// per call here is correct for one-shot reads but may read past the newline.
func ReadMessage(r io.Reader, v any) error {
	if err := json.NewDecoder(r).Decode(v); err != nil {
		return fmt.Errorf("api: read message: %w", err)
	}
	return nil
}

// Decoder reads sequential NDJSON frames off a single reader. It wraps one
// bufio.Reader so json.Decoder's read-ahead stays within this instance and does
// not consume bytes a separate decoder would need.
type Decoder struct {
	dec *json.Decoder
}

// NewDecoder returns a Decoder reading framed messages from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: json.NewDecoder(bufio.NewReader(r))}
}

// Decode reads the next frame into v. It returns io.EOF when the stream ends
// cleanly between frames; other malformed or truncated input returns a wrapped
// error.
func (d *Decoder) Decode(v any) error {
	if err := d.dec.Decode(v); err != nil {
		if err == io.EOF {
			return io.EOF
		}
		return fmt.Errorf("api: decode message: %w", err)
	}
	return nil
}

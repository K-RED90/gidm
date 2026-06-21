package api

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestFrameMalformedJSON(t *testing.T) {
	var got Request
	err := ReadMessage(strings.NewReader("{not json}\n"), &got)
	if err == nil {
		t.Fatal("ReadMessage returned nil error on malformed JSON")
	}
}

func TestFrameTruncated(t *testing.T) {
	var got Request
	err := ReadMessage(strings.NewReader(`{"version":1,"op":"add"`), &got)
	if err == nil {
		t.Fatal("ReadMessage returned nil error on truncated frame")
	}
}

func TestFrameEmpty(t *testing.T) {
	var got Request
	if err := ReadMessage(strings.NewReader(""), &got); err == nil {
		t.Fatal("ReadMessage returned nil error on empty input")
	}
}

func TestWriteMessageIsNewlineDelimited(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, NewPingRequest()); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("frame = %q, want trailing newline", buf.String())
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Errorf("frame has %d newlines, want 1", strings.Count(buf.String(), "\n"))
	}
}

func TestDecoderSequentialFrames(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, NewAddRequest("https://a/x")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if err := WriteMessage(&buf, NewStatusRequest("d1")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	dec := NewDecoder(&buf)
	var first Request
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("Decode first: %v", err)
	}
	if first.Op != OpAdd || first.Add == nil || first.Add.URL != "https://a/x" {
		t.Fatalf("first frame = %+v", first)
	}
	var second Request
	if err := dec.Decode(&second); err != nil {
		t.Fatalf("Decode second: %v", err)
	}
	if second.Op != OpStatus || second.Status == nil || second.Status.ID != "d1" {
		t.Fatalf("second frame = %+v", second)
	}
	var third Request
	if err := dec.Decode(&third); err != io.EOF {
		t.Fatalf("Decode after last = %v, want io.EOF", err)
	}
}

func TestDecoderMalformed(t *testing.T) {
	dec := NewDecoder(strings.NewReader("{oops}\n"))
	var got Request
	if err := dec.Decode(&got); err == nil {
		t.Fatal("Decode returned nil error on malformed frame")
	}
}

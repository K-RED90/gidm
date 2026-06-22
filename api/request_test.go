package api

import (
	"bytes"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		req  Request
	}{
		{"add", NewAddRequest("https://example.com/file.iso")},
		{"list", NewListRequest()},
		{"status", NewStatusRequest("d1")},
		{"pause", NewPauseRequest("d1")},
		{"resume", NewResumeRequest("d1")},
		{"rm", NewRmRequest("d1")},
		{"ping", NewPingRequest()},
		{"unknown verb", Request{Version: Version, Op: Op("frobnicate")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteMessage(&buf, tt.req); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}
			var got Request
			if err := ReadMessage(&buf, &got); err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}
			if got.Version != tt.req.Version || got.Op != tt.req.Op {
				t.Fatalf("envelope = %+v, want %+v", got, tt.req)
			}
			if (tt.req.Add == nil) != (got.Add == nil) {
				t.Fatalf("add payload presence mismatch: got %+v want %+v", got.Add, tt.req.Add)
			}
			if tt.req.Add != nil && got.Add.URL != tt.req.Add.URL {
				t.Errorf("add url = %q, want %q", got.Add.URL, tt.req.Add.URL)
			}
			if (tt.req.Status == nil) != (got.Status == nil) {
				t.Fatalf("status payload presence mismatch: got %+v want %+v", got.Status, tt.req.Status)
			}
			if tt.req.Status != nil && got.Status != nil && got.Status.ID != tt.req.Status.ID {
				t.Errorf("status id = %q, want %q", got.Status.ID, tt.req.Status.ID)
			}
			if (tt.req.Pause == nil) != (got.Pause == nil) {
				t.Fatalf("pause payload presence mismatch: got %+v want %+v", got.Pause, tt.req.Pause)
			}
			if tt.req.Pause != nil && got.Pause != nil && got.Pause.ID != tt.req.Pause.ID {
				t.Errorf("pause id = %q, want %q", got.Pause.ID, tt.req.Pause.ID)
			}
			if (tt.req.Resume == nil) != (got.Resume == nil) {
				t.Fatalf("resume payload presence mismatch: got %+v want %+v", got.Resume, tt.req.Resume)
			}
			if tt.req.Resume != nil && got.Resume != nil && got.Resume.ID != tt.req.Resume.ID {
				t.Errorf("resume id = %q, want %q", got.Resume.ID, tt.req.Resume.ID)
			}
			if (tt.req.Rm == nil) != (got.Rm == nil) {
				t.Fatalf("rm payload presence mismatch: got %+v want %+v", got.Rm, tt.req.Rm)
			}
			if tt.req.Rm != nil && got.Rm != nil && got.Rm.ID != tt.req.Rm.ID {
				t.Errorf("rm id = %q, want %q", got.Rm.ID, tt.req.Rm.ID)
			}
		})
	}
}

func TestNewRequestStampsVersion(t *testing.T) {
	if got := NewAddRequest("https://x").Version; got != Version {
		t.Errorf("Version = %d, want %d", got, Version)
	}
}

func TestAddAndSetPriorityRoundTrip(t *testing.T) {
	// add carries an explicit priority...
	add := NewAddRequestWithPriority("https://example.com/f", PriorityHigh)
	var gotAdd Request
	roundTripRequest(t, add, &gotAdd)
	if gotAdd.Add == nil || gotAdd.Add.Priority != PriorityHigh {
		t.Errorf("add priority = %+v, want high", gotAdd.Add)
	}

	// ...and set-priority round-trips id + level.
	sp := NewSetPriorityRequest("d1", PriorityLow)
	if sp.Op != OpSetPriority {
		t.Errorf("op = %q, want %q", sp.Op, OpSetPriority)
	}
	var gotSP Request
	roundTripRequest(t, sp, &gotSP)
	if gotSP.SetPriority == nil || gotSP.SetPriority.ID != "d1" || gotSP.SetPriority.Priority != PriorityLow {
		t.Errorf("set-priority payload = %+v, want {d1 low}", gotSP.SetPriority)
	}
}

func roundTripRequest(t *testing.T, req Request, dst *Request) {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteMessage(&buf, req); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if err := ReadMessage(&buf, dst); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
}

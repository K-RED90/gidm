package api

import (
	"bytes"
	"errors"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestValidateSetConfig(t *testing.T) {
	tests := []struct {
		name    string
		sc      SetConfig
		wantErr error // nil = valid
	}{
		{"empty patch ok", SetConfig{}, nil},
		{"full valid", SetConfig{
			DownloadDir:         ptr("/srv/dl"),
			SegmentsPerDownload: ptr(8),
			DefaultPriority:     ptr(PriorityHigh),
			MaxRate:             ptr(1 << 20),
			PerDownloadMaxRate:  ptr(0),
		}, nil},
		{"relative dir", SetConfig{DownloadDir: ptr("rel/dir")}, ErrInvalidDestination},
		{"dir with dotdot", SetConfig{DownloadDir: ptr("/srv/../etc")}, ErrInvalidDestination},
		{"empty dir rejected", SetConfig{DownloadDir: ptr("")}, ErrInvalidDestination},
		{"segments zero", SetConfig{SegmentsPerDownload: ptr(0)}, ErrInvalidSegments},
		{"segments too many", SetConfig{SegmentsPerDownload: ptr(65)}, ErrInvalidSegments},
		{"bad priority", SetConfig{DefaultPriority: ptr(Priority("urgent"))}, ErrInvalidPriority},
		{"empty priority rejected", SetConfig{DefaultPriority: ptr(Priority(""))}, ErrInvalidPriority},
		{"negative max rate", SetConfig{MaxRate: ptr(-1)}, ErrInvalidRate},
		{"negative per-download rate", SetConfig{PerDownloadMaxRate: ptr(-5)}, ErrInvalidRate},
		{"zero rates ok", SetConfig{MaxRate: ptr(0), PerDownloadMaxRate: ptr(0)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSetConfig(tt.sc)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSetRate(t *testing.T) {
	if err := ValidateSetRate(SetRate{ID: "abc", MaxRate: 1 << 20}); err != nil {
		t.Errorf("valid set-rate: %v", err)
	}
	if err := ValidateSetRate(SetRate{ID: "abc", MaxRate: 0}); err != nil {
		t.Errorf("zero rate (remove cap): %v", err)
	}
	if err := ValidateSetRate(SetRate{ID: "", MaxRate: 1}); !errors.Is(err, ErrEmptyID) {
		t.Errorf("empty id: err = %v, want ErrEmptyID", err)
	}
	if err := ValidateSetRate(SetRate{ID: "abc", MaxRate: -1}); !errors.Is(err, ErrInvalidRate) {
		t.Errorf("negative rate: err = %v, want ErrInvalidRate", err)
	}
}

// Constructors must stamp the version/op and survive a frame round-trip with their
// payload pointer set (and others nil).
func TestConfigRequestsRoundTrip(t *testing.T) {
	get := NewGetConfigRequest()
	if get.Op != OpGetConfig || get.Version != Version || get.SetConfig != nil {
		t.Errorf("NewGetConfigRequest = %+v", get)
	}

	sc := SetConfig{MaxRate: ptr(2 << 20), DefaultPriority: ptr(PriorityLow)}
	set := NewSetConfigRequest(sc)
	if set.Op != OpSetConfig || set.SetConfig == nil || set.SetConfig.MaxRate == nil || *set.SetConfig.MaxRate != 2<<20 {
		t.Fatalf("NewSetConfigRequest = %+v", set)
	}

	rate := NewSetRateRequest("dl1", 4096)
	if rate.Op != OpSetRate || rate.SetRate == nil || rate.SetRate.ID != "dl1" || rate.SetRate.MaxRate != 4096 {
		t.Fatalf("NewSetRateRequest = %+v", rate)
	}

	for _, req := range []Request{get, set, rate} {
		var buf bytes.Buffer
		if err := WriteMessage(&buf, req); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
		var got Request
		if err := NewDecoder(&buf).Decode(&got); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got.Op != req.Op {
			t.Errorf("round-trip op = %q, want %q", got.Op, req.Op)
		}
	}
}

func TestConfigResponseRoundTrip(t *testing.T) {
	want := ConfigView{
		DownloadDir:         "/srv/dl",
		SegmentsPerDownload: 8,
		DefaultPriority:     PriorityHigh,
		MaxRate:             1 << 20,
		PerDownloadMaxRate:  512 << 10,
	}
	resp := ConfigResponse(want)
	if !resp.OK || resp.Config == nil {
		t.Fatalf("ConfigResponse = %+v", resp)
	}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, resp); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	var got Response
	if err := NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Config == nil || got.Config.Config != want {
		t.Fatalf("round-trip config = %+v, want %+v", got.Config, want)
	}
}

package api

import (
	"bytes"
	"reflect"
	"testing"
)

func TestResponseRoundTrip(t *testing.T) {
	views := []DownloadView{
		{ID: "d1", URL: "https://a/x", Status: StatusActive, TotalSize: 100, Downloaded: 40, Destination: "/tmp/x"},
		{ID: "d2", URL: "https://a/y", Status: StatusCompleted, TotalSize: 200, Downloaded: 200, Destination: "/tmp/y"},
	}
	tests := []struct {
		name string
		resp Response
	}{
		{"ok ack", OKResponse()},
		{"add", AddResponse("d1")},
		{"list", ListResponse(views)},
		{"status", StatusResponse(views[0])},
		{"ping", PingResponse()},
		{"err not_found", ErrorResponse(CodeNotFound, "download %q not found", "d9")},
		{"err bad_request", ErrorResponse(CodeBadRequest, "missing url")},
		{"err internal", ErrorResponse(CodeInternal, "boom")},
		{"err unsupported_version", ErrorResponse(CodeUnsupportedVersion, "version %d unsupported", 99)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteMessage(&buf, tt.resp); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}
			var got Response
			if err := ReadMessage(&buf, &got); err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}
			if got.Version != Version {
				t.Errorf("Version = %d, want %d", got.Version, Version)
			}
			if got.OK != tt.resp.OK {
				t.Fatalf("OK = %v, want %v", got.OK, tt.resp.OK)
			}
			if tt.resp.OK && got.Error != nil {
				t.Errorf("ok response carried error %+v", got.Error)
			}
			if tt.resp.Error != nil {
				if got.Error == nil {
					t.Fatalf("error response decoded with nil Error")
				}
				if got.Error.Code != tt.resp.Error.Code {
					t.Errorf("Code = %q, want %q", got.Error.Code, tt.resp.Error.Code)
				}
				if got.Error.Message != tt.resp.Error.Message {
					t.Errorf("Message = %q, want %q", got.Error.Message, tt.resp.Error.Message)
				}
			}
			if tt.resp.Add != nil && (got.Add == nil || got.Add.ID != tt.resp.Add.ID) {
				t.Errorf("add result = %+v, want %+v", got.Add, tt.resp.Add)
			}
			if tt.resp.Status != nil {
				if got.Status == nil || !reflect.DeepEqual(got.Status.Download, tt.resp.Status.Download) {
					t.Errorf("status result = %+v, want %+v", got.Status, tt.resp.Status)
				}
			}
			if tt.resp.Ping != nil && (got.Ping == nil || got.Ping.Version != Version) {
				t.Errorf("ping result = %+v, want version %d", got.Ping, Version)
			}
			if tt.resp.List != nil {
				if got.List == nil || len(got.List.Downloads) != len(tt.resp.List.Downloads) {
					t.Errorf("list result = %+v, want %+v", got.List, tt.resp.List)
				} else {
					for i := range tt.resp.List.Downloads {
						if !reflect.DeepEqual(got.List.Downloads[i], tt.resp.List.Downloads[i]) {
							t.Errorf("download[%d] = %+v, want %+v", i, got.List.Downloads[i], tt.resp.List.Downloads[i])
						}
					}
				}
			}
		})
	}
}

func TestListResponsePreservesViews(t *testing.T) {
	views := []DownloadView{
		{ID: "d1", Status: StatusQueued, TotalSize: 10},
		{ID: "d2", Status: StatusPaused, Downloaded: 5},
	}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, ListResponse(views)); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	var got Response
	if err := ReadMessage(&buf, &got); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if got.List == nil || len(got.List.Downloads) != 2 {
		t.Fatalf("downloads = %+v, want 2", got.List)
	}
	for i := range views {
		if !reflect.DeepEqual(got.List.Downloads[i], views[i]) {
			t.Errorf("download[%d] = %+v, want %+v", i, got.List.Downloads[i], views[i])
		}
	}
}

func TestErrorResponseCode(t *testing.T) {
	r := ErrorResponse(CodeUnsupportedVersion, "version %d", 7)
	if r.OK {
		t.Error("error response has OK=true")
	}
	if r.Error.Code != CodeUnsupportedVersion {
		t.Errorf("Code = %q, want %q", r.Error.Code, CodeUnsupportedVersion)
	}
	if r.Error.Message != "version 7" {
		t.Errorf("Message = %q, want %q", r.Error.Message, "version 7")
	}
}

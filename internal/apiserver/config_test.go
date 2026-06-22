package apiserver_test

import (
	"testing"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/internal/engine"
)

func TestGetConfig(t *testing.T) {
	mgr := newFakeManager()
	mgr.settings = engine.Settings{
		DownloadDir:         "/srv/dl",
		SegmentsPerDownload: 8,
		DefaultPriority:     engine.PriorityHigh,
		MaxRate:             1 << 20,
		PerDownloadMaxRate:  512 << 10,
	}
	_, sock := startServer(t, mgr)

	resp := roundTrip(t, sock, api.NewGetConfigRequest())
	if !resp.OK || resp.Config == nil {
		t.Fatalf("get-config = %+v", resp)
	}
	got := resp.Config.Config
	if got.DownloadDir != "/srv/dl" || got.SegmentsPerDownload != 8 ||
		got.DefaultPriority != api.PriorityHigh || got.MaxRate != 1<<20 || got.PerDownloadMaxRate != 512<<10 {
		t.Fatalf("config view = %+v", got)
	}
}

func TestSetConfigPatchesAndEchoes(t *testing.T) {
	mgr := newFakeManager()
	mgr.settings = engine.Settings{
		DownloadDir:         "/old",
		SegmentsPerDownload: 4,
		DefaultPriority:     engine.PriorityNormal,
		MaxRate:             0,
		PerDownloadMaxRate:  0,
	}
	_, sock := startServer(t, mgr)

	// Patch only the global rate and the dir; the rest must be left unchanged.
	dir := "/new"
	rate := 2 << 20
	resp := roundTrip(t, sock, api.NewSetConfigRequest(api.SetConfig{DownloadDir: &dir, MaxRate: &rate}))
	if !resp.OK || resp.Config == nil {
		t.Fatalf("set-config = %+v", resp)
	}
	got := resp.Config.Config
	if got.DownloadDir != "/new" || got.MaxRate != 2<<20 {
		t.Errorf("patched fields not applied: %+v", got)
	}
	if got.SegmentsPerDownload != 4 || got.DefaultPriority != api.PriorityNormal {
		t.Errorf("unpatched fields changed: %+v", got)
	}
	// The manager must have been told to persist the merged settings.
	if mgr.settings.DownloadDir != "/new" || mgr.settings.MaxRate != 2<<20 || mgr.settings.SegmentsPerDownload != 4 {
		t.Errorf("manager settings after set = %+v", mgr.settings)
	}
}

func TestSetConfigRejectsInvalid(t *testing.T) {
	mgr := newFakeManager()
	_, sock := startServer(t, mgr)

	bad := -1
	resp := roundTrip(t, sock, api.NewSetConfigRequest(api.SetConfig{MaxRate: &bad}))
	if resp.OK || resp.Error == nil || resp.Error.Code != api.CodeBadRequest {
		t.Fatalf("negative rate = %+v, want bad_request", resp)
	}
}

func TestSetRate(t *testing.T) {
	mgr := newFakeManager()
	_, sock := startServer(t, mgr)

	add := roundTrip(t, sock, api.NewAddRequest("https://example.com/f.bin"))
	if !add.OK || add.Add == nil {
		t.Fatalf("add: %+v", add)
	}
	id := add.Add.ID

	resp := roundTrip(t, sock, api.NewSetRateRequest(id, 4096))
	if !resp.OK {
		t.Fatalf("set-rate = %+v", resp)
	}
	if mgr.lastSetRate.id != id || mgr.lastSetRate.bps != 4096 {
		t.Errorf("forwarded set-rate = %+v, want {%s 4096}", mgr.lastSetRate, id)
	}

	// An unknown id surfaces as not_found.
	missing := roundTrip(t, sock, api.NewSetRateRequest("nope", 1))
	if missing.OK || missing.Error == nil || missing.Error.Code != api.CodeNotFound {
		t.Fatalf("set-rate unknown id = %+v, want not_found", missing)
	}
}

func TestSetRateRejectsInvalid(t *testing.T) {
	mgr := newFakeManager()
	_, sock := startServer(t, mgr)

	if resp := roundTrip(t, sock, api.NewSetRateRequest("", 1)); resp.OK || resp.Error.Code != api.CodeBadRequest {
		t.Errorf("empty id = %+v, want bad_request", resp)
	}
	if resp := roundTrip(t, sock, api.NewSetRateRequest("x", -1)); resp.OK || resp.Error.Code != api.CodeBadRequest {
		t.Errorf("negative rate = %+v, want bad_request", resp)
	}
}

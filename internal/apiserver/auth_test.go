package apiserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/K-RED90/gidm/api"
	"github.com/K-RED90/gidm/internal/engine"
)

func TestToViewOmitsPassword(t *testing.T) {
	d := &engine.Download{
		ID:     "x",
		URL:    "https://example.com/f",
		Status: engine.StatusFailed,
		Auth: &engine.RequestOptions{
			Username: "user",
			Password: "TOP-SECRET",
			Referer:  "https://ref/",
			Headers:  map[string]string{"X-K": "v"},
		},
	}
	v := toView(d, true)
	if v.Auth == nil {
		t.Fatal("view.Auth is nil")
	}
	if !v.Auth.HasPassword {
		t.Error("HasPassword = false, want true")
	}
	if v.Auth.Username != "user" || v.Auth.Referer != "https://ref/" {
		t.Errorf("auth view = %+v", v.Auth)
	}
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "TOP-SECRET") {
		t.Fatalf("password leaked into the wire view JSON: %s", blob)
	}
}

func TestToViewNoAuth(t *testing.T) {
	v := toView(&engine.Download{ID: "y", Status: engine.StatusQueued}, true)
	if v.Auth != nil {
		t.Errorf("view.Auth = %+v, want nil for a download with no credentials", v.Auth)
	}
}

func TestResolveSetAuthKeepsPassword(t *testing.T) {
	s := &Server{mgr: stubMgrWith("d1", &engine.RequestOptions{Username: "old", Password: "keepme"})}

	opts, err := s.resolveSetAuth(context.Background(), &api.SetAuth{
		ID:           "d1",
		Auth:         api.Credentials{Username: "newuser", Referer: "https://r/"},
		KeepPassword: true,
	})
	if err != nil {
		t.Fatalf("resolveSetAuth: %v", err)
	}
	if opts == nil || opts.Username != "newuser" || opts.Referer != "https://r/" || opts.Password != "keepme" {
		t.Fatalf("resolved opts = %+v, want username=newuser referer set password=keepme", opts)
	}
}

func TestResolveSetAuthReplacesPassword(t *testing.T) {
	s := &Server{mgr: stubMgrWith("d1", &engine.RequestOptions{Username: "old", Password: "keepme"})}

	opts, err := s.resolveSetAuth(context.Background(), &api.SetAuth{
		ID:   "d1",
		Auth: api.Credentials{Username: "u", Password: "newpass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Password != "newpass" {
		t.Fatalf("password = %q, want newpass", opts.Password)
	}
}

func TestResolveSetAuthEmptyClears(t *testing.T) {
	s := &Server{mgr: stubMgrWith("d1", nil)}

	opts, err := s.resolveSetAuth(context.Background(), &api.SetAuth{ID: "d1", Auth: api.Credentials{}})
	if err != nil {
		t.Fatal(err)
	}
	if opts != nil {
		t.Fatalf("opts = %+v, want nil (clears auth)", opts)
	}
}

// stubMgr is a minimal Manager whose only meaningful method is Get, so an
// internal test can exercise toView/resolveSetAuth without the external
// apiserver_test fakeManager.
type stubMgr struct{ downloads map[string]*engine.Download }

func stubMgrWith(id string, auth *engine.RequestOptions) *stubMgr {
	return &stubMgr{downloads: map[string]*engine.Download{id: {ID: id, Auth: auth}}}
}

func (m *stubMgr) Get(_ context.Context, id string) (*engine.Download, error) {
	if d, ok := m.downloads[id]; ok {
		return d, nil
	}
	return nil, engine.ErrNotFound
}
func (m *stubMgr) Submit(context.Context, string, engine.Priority, engine.AddOptions) (string, error) {
	return "", nil
}
func (m *stubMgr) List(context.Context) ([]*engine.Download, error)              { return nil, nil }
func (m *stubMgr) Pause(context.Context, string) error                           { return nil }
func (m *stubMgr) Resume(context.Context, string) error                          { return nil }
func (m *stubMgr) Delete(context.Context, string) error                          { return nil }
func (m *stubMgr) SetPriority(context.Context, string, engine.Priority) error    { return nil }
func (m *stubMgr) SetRate(context.Context, string, int) error                    { return nil }
func (m *stubMgr) Settings() engine.Settings                                     { return engine.Settings{} }
func (m *stubMgr) SetSettings(context.Context, engine.Settings) error            { return nil }
func (m *stubMgr) SetAuth(context.Context, string, *engine.RequestOptions) error { return nil }
func (m *stubMgr) Restart(context.Context, string) error                         { return nil }

package judge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aclemen1/due-cli/internal/config"
	"github.com/aclemen1/due-cli/internal/connect"
)

func TestFallbackAndCache(t *testing.T) {
	t.Setenv("DUE_STATE", t.TempDir())
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"overloaded"}`, http.StatusServiceUnavailable)
	}))
	defer down.Close()
	var got map[string]any
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"model":"openjev","answers":{"critical":{"type":"noul","noul":0.82},"nature":{"type":"choice","choice":"legal","confidence":0.9}}}`))
	}))
	defer local.Close()

	c, err := New(config.Judge{Providers: []config.Provider{{Name: "jev", Endpoint: down.URL}, {Name: "openjev", Endpoint: local.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.Local)
	it := connect.Item{Sphere: "perso", Source: "mnemo", Type: "command", ID: "x#0", Title: "Délai de recours", At: now.AddDate(0, 0, 20), AllDay: true}
	v, err := c.Judge(context.Background(), it, now)
	if err != nil || v.Model != "openjev" || v.Critical != 0.82 || v.Nature != "legal" {
		t.Fatalf("fallback: %+v %v", v, err)
	}
	if st := got["state"].(map[string]any); st["title"] != "Délai de recours" || st["body"] != nil {
		t.Fatalf("state sent: %v", st)
	}

	cache := Load()
	cache.Put(it, v)
	if err := cache.Save(now); err != nil {
		t.Fatal(err)
	}
	items := []connect.Item{it, {Sphere: "perso", Source: "mnemo", ID: "y", Title: "Autre", At: now}}
	Annotate(items)
	if !IsCritical(items[0], 0.5) || items[0].Nature != "legal" || items[1].Critical != nil {
		t.Fatalf("annotate: %+v", items)
	}
	it.Title = "Délai de recours (modifié)"
	if _, ok := Load().Get(it); ok {
		t.Fatal("a new title must be judged again")
	}
}

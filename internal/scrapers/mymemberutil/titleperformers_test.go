package mymemberutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTitlePerformers(t *testing.T) {
	known := map[string]bool{"katija cortez": true, "coco bae": true, "kay": true}
	cases := map[string][]string{
		"Katija Cortez & Coco Bae Fucking":   {"Coco Bae", "Katija Cortez"},
		"KATIJA CORTEZ GETS A FACIAL":        {"KATIJA CORTEZ"},
		"Kayla Lea & Brad Banks":             nil,
		"Fucking The Pizza Delivery Girl":    nil,
		"Brand new katija cortez, cum shots": {"katija cortez"},
	}
	for title, want := range cases {
		got := TitlePerformers(title, known)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("TitlePerformers(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestBuildSceneTitlePerformersAndDropDescription(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><meta property="og:description" content="Storefront blurb"/></head></html>`))
	}))
	defer ts.Close()

	cfg := testConfig()
	cfg.TitlePerformers = true
	cfg.DropDescription = func(d string) bool { return d == "Storefront blurb" }
	s := New(cfg)
	s.Client = ts.Client()
	s.SiteBase = ts.URL

	vid := makeTestVideos()[0]
	vid.Title = "Alice Test Gets Covered"
	scene, err := s.BuildScene(context.Background(), ts.URL, vid)
	if err != nil {
		t.Fatal(err)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Alice Test" {
		t.Errorf("Performers = %v", scene.Performers)
	}
	if scene.Description != "" {
		t.Errorf("Description = %q, want empty", scene.Description)
	}
}

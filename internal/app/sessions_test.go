package app

import (
	"context"
	"testing"
)

func TestHideRestoreKeepsDataAndReusesSample(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := a.HideSessions(ctx); err != nil || n != 1 {
		t.Fatalf("hide: %d %v", n, err)
	}
	if list, _ := a.Sessions(ctx); len(list) != 0 {
		t.Fatalf("visible after hide: %d", len(list))
	}
	if _, err := a.Analysis(ctx, demo.LatestAnalysisID); err != nil {
		t.Fatalf("hidden sessions keep their analysis: %v", err)
	}
	// Opening the sample again shows the saved one instead of making another.
	again, err := a.CreateDemo(ctx, false)
	if err != nil || again.ID != demo.ID {
		t.Fatalf("sample reopened as %s (%v), want %s", again.ID, err, demo.ID)
	}
	if list, _ := a.Sessions(ctx); len(list) != 1 {
		t.Fatalf("visible after reopening the sample: %d", len(list))
	}
	a.HideSessions(ctx)
	if n, err := a.RestoreSessions(ctx); err != nil || n != 1 {
		t.Fatalf("restore: %d %v", n, err)
	}
	if n, _ := a.RestoreSessions(ctx); n != 0 {
		t.Fatalf("second restore changed %d", n)
	}
}

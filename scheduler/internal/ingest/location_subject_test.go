package ingest

import (
	"context"
	"testing"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/models"
)

func TestVerifyLocationResultBindsBrokerSubject(t *testing.T) {
	i := &Ingest{config: &config.SchedulerConfig{CheckResultSubject: "results"}}
	if err := i.verifyLocationResult(context.Background(), "results", models.CheckResultMessage{}); err != nil {
		t.Fatalf("platform result rejected: %v", err)
	}
	if err := i.verifyLocationResult(context.Background(), "results", models.CheckResultMessage{LocationID: "location-a"}); err == nil {
		t.Fatal("location payload accepted on platform subject")
	}
	if err := i.verifyLocationResult(context.Background(), "results.loc.location-a", models.CheckResultMessage{LocationID: "location-b"}); err == nil {
		t.Fatal("payload accepted on another location's subject")
	}
	if err := i.verifyLocationResult(context.Background(), "unexpected", models.CheckResultMessage{}); err == nil {
		t.Fatal("unexpected result subject accepted")
	}
}

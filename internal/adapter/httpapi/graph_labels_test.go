package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/analyticsconfig"
	"github.com/daniil/kb-engine/internal/adapter/changelog"
	"github.com/daniil/kb-engine/internal/adapter/httpapi"
	"github.com/daniil/kb-engine/internal/usecase/analytics"
)

// curatedAnalytics returns a topology where one edge is written in the reverse
// direction from the config, so the endpoint has to match both ways.
type curatedAnalytics struct{ fakeAnalytics }

func (curatedAnalytics) Graph() (analytics.Graph, error) {
	return analytics.Graph{
		Nodes: []analytics.GraphNode{{Category: "claude-ecosystem", Count: 117}},
		Edges: []analytics.GraphEdge{
			{From: "ai-agents-tools", To: "claude-ecosystem", Weight: 109},
			{From: "devops", To: "golang", Weight: 7},
		},
	}, nil
}

// /api/graph is where the two halves have to meet: the computed topology and
// the owner's labels. Until they did, the labels existed in the file and on no
// screen.
func TestGraphEndpoint_carriesCuratedLabels(t *testing.T) {
	srv := httpapi.NewServer(fakeQuery{}, fakeAudit{}, curatedAnalytics{}, fakeFinance{},
		func() (analyticsconfig.Config, error) {
			return analyticsconfig.Config{Graph: []analytics.CuratedLink{
				{From: "claude-ecosystem", To: "ai-agents-tools", Label: "MCP/протоколы"},
			}}, nil
		},
		func() (changelog.Document, error) { return changelog.Document{}, nil },
		httpapi.Documents{}, testEngine, nil)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/graph", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var got analytics.Graph
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if got.Edges[0].Label != "MCP/протоколы" {
		t.Errorf("edge label = %q, want the curated one (direction must not matter)", got.Edges[0].Label)
	}
	if got.Labeled != 1 || got.Unlabeled != 1 {
		t.Errorf("Labeled/Unlabeled = %d/%d, want 1/1", got.Labeled, got.Unlabeled)
	}
}

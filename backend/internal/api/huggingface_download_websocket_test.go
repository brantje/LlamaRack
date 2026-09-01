package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/huggingface"
)

func TestHuggingFaceDownloadWebSocketStreamsJobUpdates(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	apiServer := httptest.NewServer(fixture.handler)
	defer apiServer.Close()

	header := http.Header{}
	header.Set("Cookie", fixture.cookie.String())
	wsURL := "ws" + strings.TrimPrefix(apiServer.URL, "http") + "/api/v1/downloads/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	var snapshot downloadSnapshotEvent
	if err := conn.ReadJSON(&snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Type != "download_snapshot" || len(snapshot.Downloads) != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	w := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/model?repo=acme%2Fdemo", nil, true)
	var detail huggingface.ModelDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || len(detail.Artifacts) != 1 {
		t.Fatalf("detail = %+v err=%v", detail, err)
	}
	w = huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads", map[string]string{
		"repo_id": "acme/demo", "artifact_id": detail.Artifacts[0].ID,
	}, true)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created downloads.Job
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	for {
		var event downloads.Event
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "download" || event.Job == nil || event.Job.ID != created.ID {
			continue
		}
		if len(event.Job.Files) != 1 {
			t.Fatalf("streamed job must include file progress: %+v", event.Job)
		}
		break
	}
}

func TestHuggingFaceDownloadWebSocketRequiresGet(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads/ws", nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("websocket method status = %d", got)
	}
}

from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    file_path = Path(path)
    text = file_path.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one match, found {count}")
    file_path.write_text(text.replace(old, new, 1))


replace_once(
    "backend/internal/observability/writeback.go",
    '''\tpersisted := 0
\tvar firstErr error
\tfor i := range batch {
\t\tentry := batch[i]
\t\tif err := s.persistWritebackBatch(ctx, []writebackEntry{entry}); err != nil {
\t\t\tif firstErr == nil {
\t\t\t\tfirstErr = err
\t\t\t}
\t\t\tif isPermanentWritebackError(err) {
\t\t\t\tslog.Error("dropping permanently invalid inference observability writeback entry", "request_id", entry.requestID, "error", err)
\t\t\t\tcontinue
\t\t\t}
\t\t\tstate.mu.Lock()
\t\t\tif _, exists := state.entries[entry.requestID]; !exists {
\t\t\t\tcopyEntry := entry
\t\t\t\tstate.entries[entry.requestID] = &copyEntry
\t\t\t\tif entry.openAIResponseID != "" {
\t\t\t\t\tstate.openAIToRequest[entry.openAIResponseID] = entry.requestID
\t\t\t\t}
\t\t\t}
\t\t\tstate.mu.Unlock()
\t\t\tcontinue
\t\t}
\t\tpersisted++
\t}
\treturn persisted, firstErr
''',
    '''\tprocessed := 0
\tvar firstErr error
\tfor i := range batch {
\t\tentry := batch[i]
\t\tif err := s.persistWritebackBatch(ctx, []writebackEntry{entry}); err != nil {
\t\t\tif isPermanentWritebackError(err) {
\t\t\t\tslog.Error("dropping permanently invalid inference observability writeback entry", "request_id", entry.requestID, "error", err)
\t\t\t\tprocessed++
\t\t\t\tcontinue
\t\t\t}
\t\t\tif firstErr == nil {
\t\t\t\tfirstErr = err
\t\t\t}
\t\t\tstate.mu.Lock()
\t\t\tif _, exists := state.entries[entry.requestID]; !exists {
\t\t\t\tcopyEntry := entry
\t\t\t\tstate.entries[entry.requestID] = &copyEntry
\t\t\t\tif entry.openAIResponseID != "" {
\t\t\t\t\tstate.openAIToRequest[entry.openAIResponseID] = entry.requestID
\t\t\t\t}
\t\t\t}
\t\t\tstate.mu.Unlock()
\t\t\tcontinue
\t\t}
\t\tprocessed++
\t}
\treturn processed, firstErr
''',
)

replace_once(
    "backend/internal/observability/writeback_coverage_test.go",
    '''\tif err := s.MarkOpenAIResponseDeleted(ctx, "resp_rich"); err != nil {
\t\tt.Fatal(err)
\t}
\tif err := s.Flush(ctx); err != nil {
''',
    '''\tif err := s.MarkOpenAIResponseDeleted(ctx, "resp_rich"); err != nil {
\t\tt.Fatal(err)
\t}
\tvar persistedBeforeFlush int
\tif err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM inference_requests WHERE instance_id='instance-rich'").Scan(&persistedBeforeFlush); err != nil {
\t\tt.Fatal(err)
\t}
\tif persistedBeforeFlush != 0 {
\t\tt.Fatalf("rich writeback persisted before explicit Flush: %d", persistedBeforeFlush)
\t}
\tif err := s.Flush(ctx); err != nil {
''',
)

replace_once(
    "backend/internal/observability/writeback_test.go",
    '''import (
\t"context"
\t"database/sql"
\t"errors"
\t"testing"
\t"time"
)
''',
    '''import (
\t"context"
\t"database/sql"
\t"errors"
\t"fmt"
\t"testing"
\t"time"
)
''',
)

writeback_test = Path("backend/internal/observability/writeback_test.go")
writeback_test.write_text(writeback_test.read_text() + '''

func TestWritebackFlushDropsPermanentFailuresAndDrainsRemainingBatches(t *testing.T) {
\ts := testService(t)
\tctx := context.Background()
\ts.startWriteback(ctx, time.Hour)

\tif _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_permanent_writeback
BEFORE INSERT ON inference_requests
WHEN NEW.endpoint='/v1/permanent'
BEGIN
\tSELECT RAISE(ABORT, 'constraint failed: permanent writeback fixture');
END`); err != nil {
\t\tt.Fatal(err)
\t}

\trecord := RequestRecord{StartedAt: 1000, InstanceID: "instance-permanent", Endpoint: "/v1/permanent", StatusCode: 500}
\tfor i := 0; i < writebackBatchSize+1; i++ {
\t\trequestID := fmt.Sprintf("req-permanent-%03d", i)
\t\tif err := s.RecordCorrelatedRequest(ctx, requestID, nil, record); err != nil {
\t\t\tt.Fatalf("buffer %s: %v", requestID, err)
\t\t}
\t}

\tif err := s.Flush(ctx); err != nil {
\t\tt.Fatalf("explicit Flush should drop permanent failures and continue draining: %v", err)
\t}
\tstate := writebackStateFor(s)
\tstate.mu.Lock()
\tpending := len(state.entries)
\tstate.mu.Unlock()
\tif pending != 0 {
\t\tt.Fatalf("explicit Flush left %d buffered entries after permanent failures", pending)
\t}
}
''')

replace_once(
    "backend/internal/gateway/overhead_locust_test.go",
    '''\tif warmup > 0 {
\t\t_ = runGatewayLoad(t, full.URL+"/v1/chat/completions", fixture.secret, payload, users, warmup, false)
\t}

\tdirect := runGatewayLoad(t, directURL, "", payload, users, duration, true)
''',
    '''\tif warmup > 0 {
\t\twarmupResult := runGatewayLoad(t, full.URL+"/v1/chat/completions", fixture.secret, payload, users, warmup, false)
\t\trequireGatewayLoadSuccess(t, "warmup", warmupResult)
\t}

\tdirect := runGatewayLoad(t, directURL, "", payload, users, duration, true)
''',
)

replace_once(
    "backend/internal/gateway/overhead_locust_test.go",
    '''\tlogGatewayLoadResult(t, "direct", direct)
\tlogGatewayLoadResult(t, "gateway-no-observability", withoutObservability)
\tlogGatewayLoadResult(t, "gateway-observability", withObservability)
}

func runGatewayLoad''',
    '''\tlogGatewayLoadResult(t, "direct", direct)
\tlogGatewayLoadResult(t, "gateway-no-observability", withoutObservability)
\tlogGatewayLoadResult(t, "gateway-observability", withObservability)
\trequireGatewayLoadSuccess(t, "direct", direct)
\trequireGatewayLoadSuccess(t, "gateway-no-observability", withoutObservability)
\trequireGatewayLoadSuccess(t, "gateway-observability", withObservability)
}

func requireGatewayLoadSuccess(t *testing.T, name string, result gatewayLoadResult) {
\tt.Helper()
\tif result.Requests == 0 {
\t\tt.Fatalf("%s load completed without requests", name)
\t}
\tif result.Errors != 0 {
\t\tt.Fatalf("%s load had %d errors across %d requests", name, result.Errors, result.Requests)
\t}
}

func runGatewayLoad''',
)

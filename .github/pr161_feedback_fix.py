from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    target = Path(path)
    text = target.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected one match, found {count}")
    target.write_text(text.replace(old, new, 1))


gateway = Path("backend/internal/gateway/gateway.go")
text = gateway.read_text()
old = '''\tinstance, err := g.lifecycle.Instances().Get(r.Context(), modelID)
\tif err != nil || !instance.Enabled {
\t\twriteError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "The model does not exist")
\t\treturn
\t}
'''
new = '''\tinstance, err := g.lifecycle.Instances().Get(r.Context(), modelID)
\tif err != nil {
\t\tif errors.Is(err, sql.ErrNoRows) {
\t\t\twriteError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "The model does not exist")
\t\t} else {
\t\t\twriteError(w, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
\t\t}
\t\treturn
\t}
\tif !instance.Enabled {
\t\twriteError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", "The model does not exist")
\t\treturn
\t}
'''
if text.count(old) != 1:
    raise SystemExit(f"gateway.go: expected one getModel block, found {text.count(old)}")
gateway.write_text(text.replace(old, new, 1))

body_test = Path("backend/internal/gateway/request_body_limit_test.go")
text = body_test.read_text()
if "type countingBody struct" not in text:
    raise SystemExit("request_body_limit_test.go: countingBody helper not found")
body_test.write_text(text.replace("newCountingBody", "newTrackingBody").replace("countingBody", "trackingBody"))

replace_once(
    "backend/internal/supervisor/security_test.go",
    '''\t\t"--ctx-size", "1024",
\t\t"--model", "/tmp/other.gguf",
\t\t"--host", "0.0.0.0",
''',
    '''\t\t"--ctx-size", "1024",
\t\t"--model", "/tmp/other.gguf",
\t\t"--alias", "user-alias",
\t\t"--host", "0.0.0.0",
''',
)

replace_once(
    "backend/internal/gateway/gateway_test.go",
    '''\tw := gatewayRequest(t, g, http.MethodGet, "/v1/models", secret, "")
\tif w.Code != 500 || !strings.Contains(w.Body.String(), "database_error") {
\t\tt.Fatalf("models database failure=%d %s", w.Code, w.Body.String())
\t}
''',
    '''\tw := gatewayRequest(t, g, http.MethodGet, "/v1/models", secret, "")
\tif w.Code != 500 || !strings.Contains(w.Body.String(), "database_error") {
\t\tt.Fatalf("models database failure=%d %s", w.Code, w.Body.String())
\t}
\tw = gatewayRequest(t, g, http.MethodGet, "/v1/models/gateway-model", secret, "")
\tif w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "model_unavailable") {
\t\tt.Fatalf("model lookup database failure=%d %s", w.Code, w.Body.String())
\t}
''',
)

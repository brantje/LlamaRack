package benchmark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamaconfig"
	"github.com/brantje/llamarack/backend/internal/models"
)

type instanceReader interface {
	Get(context.Context, string) (instances.Instance, error)
}

type modelReader interface {
	GetByID(context.Context, string) (models.Model, error)
	ModelAbsolutePath(models.Model) (string, error)
	InspectGGUFArtifact(context.Context, string) (models.GGUFInspection, error)
}

type configReader interface {
	Effective(context.Context, string, string) (llamaconfig.Effective, error)
}

type capturedTarget struct {
	Instance  instances.Instance
	Model     models.Model
	Config    InstanceConfigSnapshot
	Artifact  ArtifactSnapshot
	ModelPath string
}

func captureTarget(ctx context.Context, instanceID string, instanceSource instanceReader, modelSource modelReader, configSource configReader) (capturedTarget, error) {
	if instanceSource == nil || modelSource == nil || configSource == nil {
		return capturedTarget{}, errors.New("benchmark target resolvers are not configured")
	}
	instance, err := instanceSource.Get(ctx, strings.TrimSpace(instanceID))
	if err != nil {
		return capturedTarget{}, err
	}
	model, err := modelSource.GetByID(ctx, instance.ModelID)
	if err != nil {
		return capturedTarget{}, err
	}
	effective, err := configSource.Effective(ctx, model.ID, instance.ID)
	if err != nil {
		return capturedTarget{}, err
	}
	modelPath, err := modelSource.ModelAbsolutePath(model)
	if err != nil {
		return capturedTarget{}, err
	}
	if info, err := os.Stat(modelPath); err != nil {
		return capturedTarget{}, fmt.Errorf("benchmark model artifact unavailable: %w", err)
	} else if !info.Mode().IsRegular() {
		return capturedTarget{}, errors.New("benchmark model artifact is not a regular file")
	}
	artifact, err := captureArtifact(ctx, modelSource, model)
	if err != nil {
		return capturedTarget{}, err
	}
	// Inspection suggests companions, but only the resolved Instance options
	// determine which artifacts will actually participate in this benchmark.
	for _, dependency := range []struct{ option, kind string }{{"mmproj", "mmproj"}, {"spec-draft-model", "draft"}} {
		path := strings.TrimSpace(effective.Values[dependency.option])
		if path == "" {
			continue
		}
		selected, err := captureArtifact(ctx, modelSource, models.Model{GGUFPath: path})
		if err != nil {
			return capturedTarget{}, fmt.Errorf("benchmark --%s dependency: %w", dependency.option, err)
		}
		artifact.Dependencies = append(artifact.Dependencies, ArtifactDependencySnapshot{
			Kind: dependency.kind, Name: filepath.Base(selected.Path), Quantization: selected.Quantization, Files: selected.Files,
		})
	}
	artifact.Fingerprint = artifactFingerprint(artifact)
	return capturedTarget{
		Instance: instance,
		Model:    model,
		Config: InstanceConfigSnapshot{
			SchemaVersion: ConfigSchemaVersion,
			GPUMode:       instance.GPUMode,
			GPUDevices:    append([]string(nil), instance.GPUDevices...),
			TensorSplit:   instance.TensorSplit,
			Options:       cloneStringMap(effective.Values),
			Sources:       cloneStringMap(effective.Sources),
		},
		Artifact:  artifact,
		ModelPath: modelPath,
	}, nil
}

func captureArtifact(ctx context.Context, modelSource modelReader, model models.Model) (ArtifactSnapshot, error) {
	inspection, err := modelSource.InspectGGUFArtifact(ctx, model.GGUFPath)
	if err != nil {
		return ArtifactSnapshot{}, fmt.Errorf("inspect benchmark artifact: %w", err)
	}
	if !inspection.Complete || inspection.ExpectedShards <= 0 || inspection.ShardCount != inspection.ExpectedShards {
		return ArtifactSnapshot{}, errors.New("registered benchmark GGUF artifact is incomplete")
	}
	mainFiles := inspection.Files
	if inspection.ShardCount > 0 && len(mainFiles) > inspection.ShardCount {
		mainFiles = mainFiles[:inspection.ShardCount]
	}
	if len(mainFiles) == 0 {
		mainFiles = []models.GGUFArtifactFile{{Path: model.GGUFPath, Size: model.TotalBytes}}
	}

	artifact := ArtifactSnapshot{
		Path:           filepath.ToSlash(filepath.Clean(model.GGUFPath)),
		Size:           inspection.ModelBytes,
		Quantization:   firstNonEmpty(model.Quantization, inspection.Quantization),
		Architecture:   inspection.Architecture,
		ShardCount:     inspection.ShardCount,
		ExpectedShards: inspection.ExpectedShards,
	}
	for _, file := range mainFiles {
		snapshot, err := captureArtifactFile(ctx, modelSource, file)
		if err != nil {
			return ArtifactSnapshot{}, err
		}
		artifact.Files = append(artifact.Files, snapshot)
		if inspection.ModelBytes <= 0 {
			artifact.Size += snapshot.Size
		}
	}
	artifact.Fingerprint = artifactFingerprint(artifact)
	return artifact, nil
}

func captureArtifactFile(ctx context.Context, modelSource modelReader, file models.GGUFArtifactFile) (ArtifactFileSnapshot, error) {
	rel := filepath.ToSlash(filepath.Clean(file.Path))
	absolute, err := modelSource.ModelAbsolutePath(models.Model{GGUFPath: file.Path})
	if err != nil {
		return ArtifactFileSnapshot{}, err
	}
	sha, size, err := hashFile(ctx, absolute)
	if err != nil {
		return ArtifactFileSnapshot{}, fmt.Errorf("fingerprint benchmark artifact %s: %w", rel, err)
	}
	if file.Size > 0 && size != file.Size {
		return ArtifactFileSnapshot{}, fmt.Errorf("benchmark artifact %s changed during resolution", rel)
	}
	return ArtifactFileSnapshot{Path: rel, Size: size, SHA256: sha}, nil
}

func hashFile(ctx context.Context, path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !before.Mode().IsRegular() {
		return "", 0, errors.New("artifact is not a regular file")
	}
	digest := sha256.New()
	written, err := copyHashContext(ctx, digest, file)
	if err != nil {
		return "", 0, err
	}
	after, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || written != after.Size() {
		return "", 0, errors.New("artifact changed while fingerprinting")
	}
	return hex.EncodeToString(digest.Sum(nil)), written, nil
}

func copyHashContext(ctx context.Context, dst hash.Hash, src io.Reader) (int64, error) {
	buffer := make([]byte, 1024*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			if _, err := dst.Write(buffer[:n]); err != nil {
				return total, err
			}
			total += int64(n)
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func artifactFingerprint(artifact ArtifactSnapshot) string {
	digest := sha256.New()
	files := append([]ArtifactFileSnapshot(nil), artifact.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, file := range files {
		writeFingerprintPart(digest, "model", file)
	}
	dependencies := append([]ArtifactDependencySnapshot(nil), artifact.Dependencies...)
	sort.Slice(dependencies, func(i, j int) bool {
		if dependencies[i].Kind != dependencies[j].Kind {
			return dependencies[i].Kind < dependencies[j].Kind
		}
		return dependencies[i].Name < dependencies[j].Name
	})
	for _, dependency := range dependencies {
		depFiles := append([]ArtifactFileSnapshot(nil), dependency.Files...)
		sort.Slice(depFiles, func(i, j int) bool { return depFiles[i].Path < depFiles[j].Path })
		for _, file := range depFiles {
			writeFingerprintPart(digest, "dependency:"+dependency.Kind, file)
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeFingerprintPart(dst io.Writer, kind string, file ArtifactFileSnapshot) {
	_, _ = fmt.Fprintf(dst, "%s\x00%s\x00%d\x00%s\n", kind, file.Path, file.Size, file.SHA256)
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func captureCPU(config InstanceConfigSnapshot) CPUSnapshot {
	threads := 0
	if raw := strings.TrimSpace(config.Options["threads"]); raw != "" {
		threads, _ = strconv.Atoi(raw)
		if threads < 0 {
			threads = 0
		}
	}
	return CPUSnapshot{
		Model:            readCPUModel(),
		LogicalThreads:   runtime.NumCPU(),
		EffectiveThreads: threads,
		Architecture:     runtime.GOARCH,
		OS:               runtime.GOOS,
	}
}

func readCPUModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		if key == "model name" || key == "hardware" {
			if value := strings.TrimSpace(parts[1]); value != "" {
				return value
			}
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

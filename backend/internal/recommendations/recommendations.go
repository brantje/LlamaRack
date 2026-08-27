package recommendations

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/scheduler"
)

const (
	mib                = int64(1024 * 1024)
	defaultContext     = 4096
	defaultVRAMReserve = 512 * mib
	defaultRAMReserve  = 1024 * mib
)

type Metadata struct {
	Architecture  string `json:"architecture,omitempty"`
	ContextLength int64  `json:"context_length,omitempty"`
	BlockCount    int64  `json:"block_count,omitempty"`
	Embedding     int64  `json:"embedding_length,omitempty"`
	HeadCount     int64  `json:"head_count,omitempty"`
	KVHeadCount   int64  `json:"kv_head_count,omitempty"`
	KeyLength     int64  `json:"key_length,omitempty"`
	ValueLength   int64  `json:"value_length,omitempty"`
}

type QuantizationInfo struct {
	Name     string `json:"name,omitempty"`
	Summary  string `json:"summary"`
	Tradeoff string `json:"tradeoff"`
}

type MemoryEstimate struct {
	WeightsBytes         int64 `json:"weights_bytes"`
	KVCacheBytes         int64 `json:"kv_cache_bytes"`
	RuntimeOverheadBytes int64 `json:"runtime_overhead_bytes"`
	CPUOnlyRAMBytes      int64 `json:"cpu_only_ram_bytes"`
	FullOffloadVRAMBytes int64 `json:"full_offload_vram_bytes"`
}

type Offload struct {
	Mode        string   `json:"mode"`
	GPULayers   int64    `json:"gpu_layers,omitempty"`
	Devices     []string `json:"devices,omitempty"`
	TensorSplit string   `json:"tensor_split,omitempty"`
	Reason      string   `json:"reason"`
}

type Recommendation struct {
	ModelID          string           `json:"model_id"`
	ContextLength    int64            `json:"context_length"`
	ContextAssumed   bool             `json:"context_assumed"`
	Confidence       string           `json:"confidence"`
	Metadata         Metadata         `json:"metadata"`
	MetadataWarning  string           `json:"metadata_warning,omitempty"`
	HardwareWarning  string           `json:"hardware_warning,omitempty"`
	Quantization     QuantizationInfo `json:"quantization"`
	Memory           MemoryEstimate   `json:"memory"`
	CurrentFit       bool             `json:"current_fit"`
	TotalHardwareFit bool             `json:"total_hardware_fit"`
	CPUFit           bool             `json:"cpu_fit"`
	Offload          Offload          `json:"offload"`
}

func Analyze(model models.Model, path string, snapshot hardware.Snapshot, requestedContext int64, hardwareErr error) Recommendation {
	metadata, metadataErr := ReadMetadata(path)
	contextLength, assumed := chooseContext(requestedContext, int64(model.ContextLength), metadata.ContextLength)
	memory := estimateMemory(model.TotalBytes, contextLength, metadata)
	result := Recommendation{
		ModelID: model.ID, ContextLength: contextLength, ContextAssumed: assumed,
		Metadata: metadata, Quantization: ExplainQuantization(model.Quantization), Memory: memory,
	}
	if metadataErr != nil {
		result.MetadataWarning = metadataErr.Error()
	}
	if hardwareErr != nil {
		result.HardwareWarning = hardwareErr.Error()
	}
	result.Confidence = confidence(metadata, metadataErr)
	result.CPUFit = snapshot.RAMAvailableBytes > defaultRAMReserve && snapshot.RAMAvailableBytes-defaultRAMReserve >= memory.CPUOnlyRAMBytes
	result.CurrentFit, result.Offload = recommendOffload(snapshot, memory, metadata)
	total := snapshot
	for i := range total.GPUs {
		total.GPUs[i].FreeBytes = total.GPUs[i].TotalBytes
	}
	if len(total.GPUs) > 0 {
		placement, _ := scheduler.PlanPlacement(total, scheduler.PlacementRequest{RequiredBytes: memory.FullOffloadVRAMBytes})
		result.TotalHardwareFit = placement.Fits
	} else {
		result.TotalHardwareFit = snapshot.RAMTotalBytes > defaultRAMReserve && snapshot.RAMTotalBytes-defaultRAMReserve >= memory.CPUOnlyRAMBytes
	}
	if len(snapshot.GPUs) == 0 {
		result.CurrentFit = result.CPUFit
		if result.CPUFit {
			result.Offload = Offload{Mode: "cpu", Reason: "No GPU was detected; the estimated model and KV cache fit in currently available system RAM."}
		} else {
			result.Offload = Offload{Mode: "cpu", Reason: "No GPU was detected and currently available system RAM is below the conservative estimate."}
		}
	}
	return result
}

func chooseContext(requested, configured, metadata int64) (int64, bool) {
	if requested > 0 {
		return requested, false
	}
	if configured > 0 {
		return configured, false
	}
	if metadata > 0 {
		return metadata, false
	}
	return defaultContext, true
}

func estimateMemory(weights, context int64, metadata Metadata) MemoryEstimate {
	if weights < 0 {
		weights = 0
	}
	overhead := int64(math.Ceil(float64(weights) * 0.05))
	if overhead < 256*mib {
		overhead = 256 * mib
	}
	kv := estimateKV(context, metadata)
	return MemoryEstimate{
		WeightsBytes: weights, KVCacheBytes: kv, RuntimeOverheadBytes: overhead,
		CPUOnlyRAMBytes: weights + kv + overhead, FullOffloadVRAMBytes: weights + kv + overhead,
	}
}

func estimateKV(context int64, m Metadata) int64 {
	if context <= 0 || m.BlockCount <= 0 || m.Embedding <= 0 || m.HeadCount <= 0 {
		return 0
	}
	kvHeads := m.KVHeadCount
	if kvHeads <= 0 {
		kvHeads = m.HeadCount
	}
	headDim := m.Embedding / m.HeadCount
	keyDim, valueDim := m.KeyLength, m.ValueLength
	if keyDim <= 0 {
		keyDim = headDim
	}
	if valueDim <= 0 {
		valueDim = headDim
	}
	if headDim <= 0 || keyDim <= 0 || valueDim <= 0 {
		return 0
	}
	// llama.cpp defaults to f16 KV unless configured otherwise: two bytes per K/V element.
	return context * m.BlockCount * kvHeads * (keyDim + valueDim) * 2
}

func recommendOffload(snapshot hardware.Snapshot, memory MemoryEstimate, metadata Metadata) (bool, Offload) {
	if len(snapshot.GPUs) == 0 {
		return false, Offload{}
	}
	placement, _ := scheduler.PlanPlacement(snapshot, scheduler.PlacementRequest{RequiredBytes: memory.FullOffloadVRAMBytes})
	if placement.Fits {
		mode := "full"
		reason := "The full model, estimated KV cache and runtime headroom fit with the scheduler VRAM reserve."
		if len(placement.Devices) > 1 {
			mode = "multi_gpu"
			reason = "No single GPU fits the full estimate, but the scheduler can place it across the minimum practical GPU set."
		}
		return true, Offload{Mode: mode, GPULayers: metadata.BlockCount, Devices: placement.Devices, TensorSplit: placement.TensorSplit, Reason: reason}
	}
	bestID := ""
	var best int64
	for _, gpu := range snapshot.GPUs {
		usable := gpu.FreeBytes - defaultVRAMReserve
		if usable > best {
			best, bestID = usable, gpu.ID
		}
	}
	fixed := memory.KVCacheBytes + memory.RuntimeOverheadBytes
	if best > fixed && memory.WeightsBytes > 0 {
		fraction := float64(best-fixed) / float64(memory.WeightsBytes)
		if fraction > 1 {
			fraction = 1
		}
		if fraction > 0 {
			layers := int64(0)
			if metadata.BlockCount > 0 {
				layers = int64(math.Floor(float64(metadata.BlockCount) * fraction))
				if layers < 1 {
					layers = 1
				}
			}
			return false, Offload{Mode: "partial", GPULayers: layers, Devices: []string{bestID}, Reason: "Full offload does not fit currently; use the largest single-GPU partial offload that preserves KV/runtime headroom."}
		}
	}
	return false, Offload{Mode: "cpu", Reason: "Current free VRAM is below the conservative KV/runtime requirement; prefer CPU loading or free VRAM first."}
}

func confidence(m Metadata, err error) string {
	if err == nil && m.BlockCount > 0 && m.Embedding > 0 && m.HeadCount > 0 {
		return "high"
	}
	if m.Architecture != "" || m.ContextLength > 0 || m.BlockCount > 0 {
		return "medium"
	}
	return "low"
}

func ExplainQuantization(value string) QuantizationInfo {
	q := strings.ToUpper(strings.TrimSpace(value))
	info := QuantizationInfo{Name: q, Summary: "Quantization is unknown; memory estimates use the actual GGUF file size.", Tradeoff: "Quality and speed vary by architecture and hardware."}
	switch {
	case strings.HasPrefix(q, "Q2"), strings.HasPrefix(q, "Q3"):
		info.Summary = "Very compact quantization aimed at minimizing RAM/VRAM use."
		info.Tradeoff = "Usually gives up more model quality than Q4+ variants in exchange for fitting smaller hardware."
	case strings.HasPrefix(q, "Q4"):
		info.Summary = "Balanced quantization with a relatively small memory footprint."
		info.Tradeoff = "A common general-purpose balance between memory use and retained model quality."
	case strings.HasPrefix(q, "Q5"):
		info.Summary = "Higher-fidelity quantization with moderate additional memory use."
		info.Tradeoff = "Often useful when Q4 fits comfortably and extra memory can be spent on quality."
	case strings.HasPrefix(q, "Q6"):
		info.Summary = "High-fidelity quantization with a larger memory footprint."
		info.Tradeoff = "Retains more precision but leaves less VRAM for context and concurrent models."
	case strings.HasPrefix(q, "Q8"):
		info.Summary = "Large quantization close to full-precision behavior for many use cases."
		info.Tradeoff = "Uses substantially more memory than Q4/Q5 variants and is rarely the best fit-first choice."
	case q == "F16", q == "BF16", q == "F32":
		info.Summary = "Full or near-full precision weights with a very large memory footprint."
		info.Tradeoff = "Best suited to hardware with abundant memory; quantized variants are usually easier to deploy."
	}
	return info
}

// ReadMetadata reads only the GGUF metadata section. Unsupported/corrupt metadata
// degrades recommendations to file-size estimates instead of making the model unusable.
func ReadMetadata(path string) (Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return Metadata{}, err
	}
	if string(magic[:]) != "GGUF" {
		return Metadata{}, errors.New("GGUF metadata unavailable: invalid magic")
	}
	version, err := readU32(f)
	if err != nil {
		return Metadata{}, err
	}
	if version < 2 || version > 3 {
		return Metadata{}, fmt.Errorf("GGUF metadata unavailable: unsupported version %d", version)
	}
	if _, err = readU64(f); err != nil {
		return Metadata{}, err
	}
	count, err := readU64(f)
	if err != nil {
		return Metadata{}, err
	}
	if count > 1_000_000 {
		return Metadata{}, errors.New("GGUF metadata unavailable: unreasonable metadata count")
	}
	values := map[string]any{}
	for i := uint64(0); i < count; i++ {
		key, err := readString(f)
		if err != nil {
			return Metadata{}, err
		}
		t, err := readU32(f)
		if err != nil {
			return Metadata{}, err
		}
		value, err := readValue(f, t, true)
		if err != nil {
			return Metadata{}, err
		}
		if relevantKey(key) {
			values[key] = value
		}
	}
	m := Metadata{Architecture: stringValue(values["general.architecture"])}
	for key, value := range values {
		switch {
		case strings.HasSuffix(key, ".context_length"):
			m.ContextLength = intValue(value)
		case strings.HasSuffix(key, ".block_count"):
			m.BlockCount = intValue(value)
		case strings.HasSuffix(key, ".embedding_length"):
			m.Embedding = intValue(value)
		case strings.HasSuffix(key, ".attention.head_count_kv"):
			m.KVHeadCount = intValue(value)
		case strings.HasSuffix(key, ".attention.head_count"):
			m.HeadCount = intValue(value)
		case strings.HasSuffix(key, ".attention.key_length"):
			m.KeyLength = intValue(value)
		case strings.HasSuffix(key, ".attention.value_length"):
			m.ValueLength = intValue(value)
		}
	}
	return m, nil
}

func relevantKey(key string) bool {
	return key == "general.architecture" || strings.HasSuffix(key, ".context_length") || strings.HasSuffix(key, ".block_count") || strings.HasSuffix(key, ".embedding_length") || strings.Contains(key, ".attention.head_count") || strings.HasSuffix(key, ".attention.key_length") || strings.HasSuffix(key, ".attention.value_length")
}

func readValue(r io.ReadSeeker, t uint32, keep bool) (any, error) {
	sizes := map[uint32]int64{0: 1, 1: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 7: 1, 10: 8, 11: 8, 12: 8}
	if size, ok := sizes[t]; ok {
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		if !keep {
			return nil, nil
		}
		switch t {
		case 0, 7:
			return int64(buf[0]), nil
		case 1:
			return int64(int8(buf[0])), nil
		case 2:
			return int64(binary.LittleEndian.Uint16(buf)), nil
		case 3:
			return int64(int16(binary.LittleEndian.Uint16(buf))), nil
		case 4:
			return int64(binary.LittleEndian.Uint32(buf)), nil
		case 5:
			return int64(int32(binary.LittleEndian.Uint32(buf))), nil
		case 10, 11:
			return int64(binary.LittleEndian.Uint64(buf)), nil
		default:
			return nil, nil
		}
	}
	switch t {
	case 8:
		value, err := readString(r)
		if !keep {
			return nil, err
		}
		return value, err
	case 9:
		elem, err := readU32(r)
		if err != nil {
			return nil, err
		}
		count, err := readU64(r)
		if err != nil {
			return nil, err
		}
		if count > 10_000_000 {
			return nil, errors.New("GGUF metadata array is unreasonable")
		}
		for i := uint64(0); i < count; i++ {
			if _, err := readValue(r, elem, false); err != nil {
				return nil, err
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("GGUF metadata has unsupported value type %d", t)
	}
}

func readString(r io.Reader) (string, error) {
	n, err := readU64(r)
	if err != nil {
		return "", err
	}
	if n > uint64(16*mib) {
		return "", errors.New("GGUF metadata string is unreasonable")
	}
	buf := make([]byte, int(n))
	_, err = io.ReadFull(r, buf)
	return string(buf), err
}

func readU32(r io.Reader) (uint32, error) {
	var v uint32
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func readU64(r io.Reader) (uint64, error) {
	var v uint64
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func intValue(v any) int64 {
	n, _ := v.(int64)
	return n
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

package hardware

import (
	"strconv"
	"strings"
)

// knownNVIDIAMemoryBusWidth is a deliberately narrow last-resort fallback for
// desktop GPUs whose driver exposes the max memory clock but not NVML's
// memory.bus_width field through nvidia-smi. Keep entries exact: laptop/OEM
// variants can have different memory interfaces and must not inherit desktop
// specifications accidentally.
func knownNVIDIAMemoryBusWidth(name string) float64 {
	normalized := strings.ToUpper(strings.Join(strings.Fields(name), " "))
	switch normalized {
	case "NVIDIA GEFORCE RTX 4060 TI":
		return 128
	default:
		return 0
	}
}

// parseNVIDIAPCIeQuery extracts max PCIe generation/width from the human-readable
// nvidia-smi -q report. Some consumer/container driver combinations expose these
// values in -q while rejecting the corresponding --query-gpu fields.
func parseNVIDIAPCIeQuery(text string) map[int]int64 {
	result := map[int]int64{}
	index := -1
	generation := 0
	width := 0
	section := ""

	flush := func() {
		if index >= 0 && generation > 0 && width > 0 {
			result[index] = theoreticalPCIeBandwidth(generation, width)
		}
		index = -1
		generation = 0
		width = 0
		section = ""
	}

	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if isNVIDIAQueryGPUHeader(line) {
			flush()
			continue
		}
		if strings.HasPrefix(line, "Minor Number") {
			if value, ok := nvidiaQueryValue(line); ok {
				if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
					index = parsed
				}
			}
			continue
		}
		switch line {
		case "PCIe Generation":
			section = "generation"
			continue
		case "Link Width":
			section = "width"
			continue
		}
		if !strings.HasPrefix(line, "Max") || section == "" {
			continue
		}
		value, ok := nvidiaQueryValue(line)
		if !ok {
			continue
		}
		value = strings.TrimSuffix(strings.TrimSpace(value), "x")
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			section = ""
			continue
		}
		if section == "generation" {
			generation = parsed
		} else {
			width = parsed
		}
		section = ""
	}
	flush()
	return result
}

func isNVIDIAQueryGPUHeader(line string) bool {
	if !strings.HasPrefix(line, "GPU ") {
		return false
	}
	identifier := strings.TrimSpace(strings.TrimPrefix(line, "GPU "))
	return strings.Contains(identifier, ":") && strings.Contains(identifier, ".")
}

func nvidiaQueryValue(line string) (string, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

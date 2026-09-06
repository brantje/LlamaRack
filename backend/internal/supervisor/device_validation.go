package supervisor

import (
	"strconv"
	"strings"
)

func launchDeviceIDs(args, env []string) []string {
	var devices []string
	for index := 0; index < len(args); index++ {
		raw := strings.TrimSpace(args[index])
		if raw == "--device" && index+1 < len(args) {
			for _, value := range strings.Split(args[index+1], ",") {
				if id := strings.TrimSpace(value); id != "" {
					devices = append(devices, id)
				}
			}
			index++
			continue
		}
		if strings.HasPrefix(raw, "--device=") {
			for _, value := range strings.Split(strings.TrimPrefix(raw, "--device="), ",") {
				if id := strings.TrimSpace(value); id != "" {
					devices = append(devices, id)
				}
			}
		}
	}
	if len(devices) != 1 {
		return devices
	}
	if devices[0] == "CUDA0" {
		if index, ok := visibleDeviceIndex(env, "CUDA_VISIBLE_DEVICES"); ok {
			return []string{"CUDA" + strconv.Itoa(index)}
		}
	}
	if devices[0] == "ROCm0" {
		if index, ok := visibleDeviceIndex(env, "HIP_VISIBLE_DEVICES"); ok {
			return []string{"ROCm" + strconv.Itoa(index)}
		}
	}
	return devices
}

func visibleDeviceIndex(env []string, key string) (int, bool) {
	prefix := key + "="
	for _, value := range env {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		visible := strings.TrimSpace(strings.TrimPrefix(value, prefix))
		if strings.Contains(visible, ",") {
			return 0, false
		}
		index, err := strconv.Atoi(visible)
		if err != nil || index < 0 {
			return 0, false
		}
		return index, true
	}
	return 0, false
}

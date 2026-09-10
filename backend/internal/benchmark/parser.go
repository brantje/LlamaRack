package benchmark

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func ParseMachineOutput(data []byte, workload WorkloadProfile) ([]Result, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errorsForParser("llama-bench returned no machine-readable rows")
	}
	rows, err := decodeMachineRows(trimmed)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errorsForParser("llama-bench returned an empty result set")
	}
	results := make([]Result, 0, len(rows))
	for index, row := range rows {
		prompt, promptPresent, err := benchmarkIdentityInt64(row, "n_prompt")
		if err != nil {
			return nil, errorsForParser(fmt.Sprintf("row %d has invalid n_prompt: %v", index, err))
		}
		generation, generationPresent, err := benchmarkIdentityInt64(row, "n_gen")
		if err != nil {
			return nil, errorsForParser(fmt.Sprintf("row %d has invalid n_gen: %v", index, err))
		}
		depth, depthPresent, err := benchmarkIdentityInt64(row, "n_depth")
		if err != nil {
			return nil, errorsForParser(fmt.Sprintf("row %d has invalid n_depth: %v", index, err))
		}
		if !promptPresent && !generationPresent && !depthPresent || prompt == 0 && generation == 0 && depth == 0 {
			return nil, errorsForParser(fmt.Sprintf("row %d is missing a valid benchmark case identity", index))
		}
		averageTokensPS, present, err := benchmarkFloat64(row, "avg_ts")
		if err != nil {
			return nil, errorsForParser(fmt.Sprintf("row %d has invalid avg_ts: %v", index, err))
		}
		if !present {
			return nil, errorsForParser(fmt.Sprintf("row %d is missing avg_ts benchmark measurement", index))
		}
		if averageTokensPS < 0 {
			return nil, errorsForParser(fmt.Sprintf("row %d has invalid avg_ts: value must be zero or greater", index))
		}

		repetitions := int(jsonInt64(row, "repetitions"))
		if repetitions <= 0 {
			repetitions = int(jsonInt64(row, "n_repetitions"))
		}
		if repetitions <= 0 {
			if samples, ok := row["samples_ts"]; ok {
				var values []json.RawMessage
				if json.Unmarshal(samples, &values) == nil {
					repetitions = len(values)
				}
			}
		}
		if repetitions <= 0 {
			repetitions = workload.Repetitions
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("encode benchmark row %d: %w", index, err)
		}
		results = append(results, Result{
			CaseIndex:        index,
			CaseID:           benchmarkCaseID(index, prompt, generation, depth),
			PromptTokens:     prompt,
			GenerationTokens: generation,
			ContextDepth:     depth,
			Repetitions:      repetitions,
			AverageNS:        jsonInt64(row, "avg_ns"),
			StdDevNS:         jsonInt64(row, "stddev_ns"),
			AverageTokensPS:  averageTokensPS,
			StdDevTokensPS:   jsonFloat64(row, "stddev_ts"),
			RawFields:        raw,
		})
	}
	return results, nil
}

func decodeMachineRows(data []byte) ([]map[string]json.RawMessage, error) {
	if len(data) > 0 && data[0] == '[' {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(data, &rows); err != nil {
			return nil, errorsForParser("invalid llama-bench JSON: " + err.Error())
		}
		return rows, nil
	}
	if len(data) > 0 && data[0] == '{' && !bytes.Contains(data, []byte("\n")) {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(data, &row); err != nil {
			return nil, errorsForParser("invalid llama-bench JSON object: " + err.Error())
		}
		return []map[string]json.RawMessage{row}, nil
	}
	var rows []map[string]json.RawMessage
	scanner := bufio.NewScanner(bytes.NewReader(data))
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row map[string]json.RawMessage
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, errorsForParser("invalid llama-bench JSONL row: " + err.Error())
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, errorsForParser("read llama-bench JSONL: " + err.Error())
	}
	return rows, nil
}

func benchmarkIdentityInt64(row map[string]json.RawMessage, key string) (int64, bool, error) {
	raw, ok := row[key]
	if !ok {
		return 0, false, nil
	}
	text, err := benchmarkNumericText(raw)
	if err != nil {
		return 0, true, err
	}
	if value, err := strconv.ParseInt(text, 10, 64); err == nil {
		if value < 0 {
			return 0, true, fmt.Errorf("value must be zero or greater")
		}
		return value, true, nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value < 0 || value > math.MaxInt64 {
		return 0, true, fmt.Errorf("expected a non-negative integer")
	}
	return int64(value), true, nil
}

func benchmarkFloat64(row map[string]json.RawMessage, key string) (float64, bool, error) {
	raw, ok := row[key]
	if !ok {
		return 0, false, nil
	}
	text, err := benchmarkNumericText(raw)
	if err != nil {
		return 0, true, err
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, true, fmt.Errorf("expected a finite number")
	}
	return value, true, nil
}

func benchmarkNumericText(raw json.RawMessage) (string, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil && number.String() != "" {
		return number.String(), nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		if text != "" {
			return text, nil
		}
	}
	return "", fmt.Errorf("expected a number")
}

func jsonInt64(row map[string]json.RawMessage, key string) int64 {
	raw := row[key]
	if len(raw) == 0 {
		return 0
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		if value, err := number.Int64(); err == nil {
			return value
		}
		if value, err := strconv.ParseFloat(number.String(), 64); err == nil {
			return int64(value)
		}
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, _ := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		return value
	}
	return 0
}

func jsonFloat64(row map[string]json.RawMessage, key string) float64 {
	raw := row[key]
	if len(raw) == 0 {
		return 0
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		value, _ := strconv.ParseFloat(number.String(), 64)
		return value
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		value, _ := strconv.ParseFloat(strings.TrimSpace(text), 64)
		return value
	}
	return 0
}

func benchmarkCaseID(index int, prompt, generation, depth int64) string {
	var base string
	switch {
	case prompt > 0 && generation > 0:
		base = fmt.Sprintf("pg-%d-%d", prompt, generation)
	case prompt > 0:
		base = fmt.Sprintf("pp-%d", prompt)
	case generation > 0:
		base = fmt.Sprintf("tg-%d", generation)
	default:
		base = fmt.Sprintf("case-%d", index+1)
	}
	if depth > 0 {
		return fmt.Sprintf("%s-d%d", base, depth)
	}
	return base
}

func errorsForParser(message string) error {
	return fmt.Errorf("benchmark parser: %s", message)
}

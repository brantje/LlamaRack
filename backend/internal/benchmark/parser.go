package benchmark

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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
		prompt := jsonInt64(row, "n_prompt")
		generation := jsonInt64(row, "n_gen")
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
			CaseID:           benchmarkCaseID(index, prompt, generation),
			PromptTokens:     prompt,
			GenerationTokens: generation,
			Repetitions:      repetitions,
			AverageNS:        jsonInt64(row, "avg_ns"),
			StdDevNS:         jsonInt64(row, "stddev_ns"),
			AverageTokensPS:  jsonFloat64(row, "avg_ts"),
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

func benchmarkCaseID(index int, prompt, generation int64) string {
	switch {
	case prompt > 0 && generation > 0:
		return fmt.Sprintf("pg-%d-%d", prompt, generation)
	case prompt > 0:
		return fmt.Sprintf("pp-%d", prompt)
	case generation > 0:
		return fmt.Sprintf("tg-%d", generation)
	default:
		return fmt.Sprintf("case-%d", index+1)
	}
}

func errorsForParser(message string) error {
	return fmt.Errorf("benchmark parser: %s", message)
}

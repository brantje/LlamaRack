package huggingface

import "regexp"

func init() {
	// Repository filenames are not consistent about separators; accept spaces and
	// other punctuation around the quantization token as well as dashes/underscores.
	quantPattern = regexp.MustCompile(`(?i)(?:^|[^A-Z0-9])(IQ\d(?:_[A-Z0-9]+)+|Q\d(?:_[A-Z0-9]+)+|BF16|F16|F32)(?:[^A-Z0-9]|$)`)
}

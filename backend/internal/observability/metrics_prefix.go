package observability

import (
	"fmt"
	"io"
)

const metricPrefix = "llamarack_"

func writeMetricHelp(w io.Writer, suffix, help string) {
	fmt.Fprintf(w, "# HELP %s%s %s\n", metricPrefix, suffix, help)
}

func writeMetricType(w io.Writer, suffix, kind string) {
	fmt.Fprintf(w, "# TYPE %s%s %s\n", metricPrefix, suffix, kind)
}

func writeMetricSample(w io.Writer, suffix, labels, value string) {
	if labels == "" {
		fmt.Fprintf(w, "%s%s %s\n", metricPrefix, suffix, value)
		return
	}
	fmt.Fprintf(w, "%s%s%s %s\n", metricPrefix, suffix, labels, value)
}

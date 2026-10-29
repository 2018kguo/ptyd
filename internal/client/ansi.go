package client

import (
	"bytes"
	"strconv"
	"strings"
)

// WithoutANSIColors keeps layout and character attributes (including conceal)
// while removing color changes from a canonical rendered row.
func WithoutANSIColors(data []byte) []byte {
	var scan sequenceReader
	out := make([]byte, 0, len(data))
	scan.feed(data, func(p []byte, osc bool) {
		if !osc && bytes.HasPrefix(p, []byte("\x1b[")) && p[len(p)-1] == 'm' {
			params := strings.Split(string(p[2:len(p)-1]), ";")
			kept := make([]string, 0, len(params))
			for i := 0; i < len(params); i++ {
				part := params[i]
				if part == "38" || part == "48" || part == "58" {
					if i+1 < len(params) {
						switch params[i+1] {
						case "5":
							i += 2
						case "2":
							i += 4
						}
					}
					continue
				}
				if strings.HasPrefix(part, "38:") || strings.HasPrefix(part, "48:") || strings.HasPrefix(part, "58:") {
					continue
				}
				if code, e := strconv.Atoi(part); e == nil && ((code >= 30 && code <= 37) || code == 39 || (code >= 40 && code <= 47) || code == 49 || (code >= 90 && code <= 107)) {
					continue
				}
				kept = append(kept, part)
			}
			out = append(out, "\x1b["...)
			out = append(out, strings.Join(kept, ";")...)
			out = append(out, 'm')
		} else {
			out = append(out, p...)
		}
	})
	return append(out, scan.flush()...)
}

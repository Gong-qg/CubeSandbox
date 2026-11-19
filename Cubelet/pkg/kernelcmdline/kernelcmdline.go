// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

// Package kernelcmdline merges guest kernel command line params carried in the
// cube.vm.kernel.cmdline.append annotation.
package kernelcmdline

import (
	"strings"

	jsoniter "github.com/json-iterator/go"
	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/constants"
)

// MergeAnnotation stores k into dst, unioning the JSON array for the kernel
// cmdline annotation so independent producers do not overwrite each other.
func MergeAnnotation(dst map[string]string, k, v string) {
	if k != constants.AnnotationVMKernelCmdlineAppend {
		dst[k] = v
		return
	}
	dst[k] = UnionJSON(dst[k], v)
}

// UnionJSON returns the union of two JSON string arrays, keyed by the text
// before the first '='. Entries from b win on duplicate keys, and a key that
// appears more than once in either input keeps only its last value.
func UnionJSON(a, b string) string {
	var paramsA, paramsB []string
	_ = jsoniter.Unmarshal([]byte(a), &paramsA)
	_ = jsoniter.Unmarshal([]byte(b), &paramsB)

	paramKey := func(p string) string {
		if idx := strings.IndexByte(p, '='); idx >= 0 {
			return p[:idx]
		}
		return p
	}

	merged := make([]string, 0, len(paramsA)+len(paramsB))
	index := make(map[string]int, len(paramsA)+len(paramsB))
	for _, p := range paramsA {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k := paramKey(p)
		if i, ok := index[k]; ok {
			merged[i] = p
			continue
		}
		index[k] = len(merged)
		merged = append(merged, p)
	}
	for _, p := range paramsB {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		k := paramKey(p)
		if i, ok := index[k]; ok {
			merged[i] = p
			continue
		}
		index[k] = len(merged)
		merged = append(merged, p)
	}

	out, err := jsoniter.Marshal(merged)
	if err != nil {
		return b
	}
	return string(out)
}

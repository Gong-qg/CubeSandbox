// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package kernelcmdline

import (
	"testing"

	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tencentcloud/CubeSandbox/Cubelet/pkg/constants"
)

func TestUnionJSON(t *testing.T) {
	t.Run("empty existing keeps new", func(t *testing.T) {
		got := UnionJSON("", `["damon_reclaim.enabled=Y"]`)
		assert.JSONEq(t, `["damon_reclaim.enabled=Y"]`, got)
	})

	t.Run("union of video and damon", func(t *testing.T) {
		video := `["video=vfb:enable,720x1280M-32@60","vfb.videomemorysize=4423680"]`
		damon := `["damon_reclaim.enabled=Y","damon_reclaim.monitor_region_start=4096"]`
		got := UnionJSON(video, damon)
		var params []string
		require.NoError(t, jsoniter.Unmarshal([]byte(got), &params))
		assert.ElementsMatch(t, []string{
			"video=vfb:enable,720x1280M-32@60",
			"vfb.videomemorysize=4423680",
			"damon_reclaim.enabled=Y",
			"damon_reclaim.monitor_region_start=4096",
		}, params)
	})

	t.Run("duplicate key keeps later value", func(t *testing.T) {
		got := UnionJSON(
			`["damon_reclaim.enabled=Y"]`,
			`["damon_reclaim.enabled=N"]`,
		)
		assert.JSONEq(t, `["damon_reclaim.enabled=N"]`, got)
	})

	t.Run("duplicate key inside first array keeps last value", func(t *testing.T) {
		got := UnionJSON(
			`["damon_reclaim.enabled=Y","damon_reclaim.enabled=N"]`,
			"",
		)
		assert.JSONEq(t, `["damon_reclaim.enabled=N"]`, got)
	})

	t.Run("blank entries dropped", func(t *testing.T) {
		got := UnionJSON(`[" "]`, `["damon_reclaim.enabled=Y"]`)
		assert.JSONEq(t, `["damon_reclaim.enabled=Y"]`, got)
	})
}

func TestMergeAnnotation(t *testing.T) {
	t.Run("non-kernel key overwrites", func(t *testing.T) {
		dst := map[string]string{"other": "old"}
		MergeAnnotation(dst, "other", "new")
		assert.Equal(t, "new", dst["other"])
	})

	t.Run("kernel key unions", func(t *testing.T) {
		dst := map[string]string{
			constants.AnnotationVMKernelCmdlineAppend: `["video=vfb:enable,720x1280M-32@60"]`,
		}
		MergeAnnotation(dst, constants.AnnotationVMKernelCmdlineAppend,
			`["damon_reclaim.enabled=Y"]`)
		var params []string
		require.NoError(t, jsoniter.Unmarshal(
			[]byte(dst[constants.AnnotationVMKernelCmdlineAppend]), &params))
		assert.ElementsMatch(t, []string{
			"video=vfb:enable,720x1280M-32@60",
			"damon_reclaim.enabled=Y",
		}, params)
	})

	t.Run("order independent for distinct keys", func(t *testing.T) {
		video := `["video=vfb:enable,720x1280M-32@60","vfb.videomemorysize=4423680"]`
		damon := `["damon_reclaim.enabled=Y"]`

		forward := map[string]string{}
		MergeAnnotation(forward, constants.AnnotationVMKernelCmdlineAppend, video)
		MergeAnnotation(forward, constants.AnnotationVMKernelCmdlineAppend, damon)

		reverse := map[string]string{}
		MergeAnnotation(reverse, constants.AnnotationVMKernelCmdlineAppend, damon)
		MergeAnnotation(reverse, constants.AnnotationVMKernelCmdlineAppend, video)

		var fwd, rev []string
		require.NoError(t, jsoniter.Unmarshal(
			[]byte(forward[constants.AnnotationVMKernelCmdlineAppend]), &fwd))
		require.NoError(t, jsoniter.Unmarshal(
			[]byte(reverse[constants.AnnotationVMKernelCmdlineAppend]), &rev))
		assert.ElementsMatch(t, fwd, rev)
	})
}

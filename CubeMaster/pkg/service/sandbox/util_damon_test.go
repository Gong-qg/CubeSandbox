// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package sandbox

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/constants"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/service/sandbox/types"
	"github.com/tencentcloud/CubeSandbox/pkgs/proto/services/cubebox/v1"
)

func TestFilterVMKernelCmdline(t *testing.T) {
	t.Run("allowlisted params pass", func(t *testing.T) {
		in := `["page_reporting.page_reporting_order=6","damon_reclaim.enabled=Y","damon_reclaim.monitor_region_start=4096"]`
		out, err := FilterVMKernelCmdline(in)
		require.NoError(t, err)
		assert.JSONEq(t, in, out)
	})

	t.Run("reject non-allowlisted module", func(t *testing.T) {
		_, err := FilterVMKernelCmdline(`["init=/bin/sh"]`)
		require.Error(t, err)
	})

	t.Run("reject embedded whitespace injection", func(t *testing.T) {
		_, err := FilterVMKernelCmdline(`["damon_reclaim.enabled=Y init=/bin/sh"]`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("reject control characters", func(t *testing.T) {
		_, err := FilterVMKernelCmdline("[\"damon_reclaim.enabled=Y\\ninit=/bin/sh\"]")
		require.Error(t, err)
	})

	t.Run("reject shell metacharacters", func(t *testing.T) {
		_, err := FilterVMKernelCmdline(`["damon_reclaim.enabled=Y|init=/bin/sh"]`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("reject oversized payload", func(t *testing.T) {
		entries := make([]string, 0, 100)
		for i := 0; i < 100; i++ {
			entries = append(entries, fmt.Sprintf("damon_reclaim.p%d=1", i))
		}
		payload, err := json.Marshal(entries)
		require.NoError(t, err)
		_, err = FilterVMKernelCmdline(string(payload))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds")
	})

	t.Run("empty input", func(t *testing.T) {
		out, err := FilterVMKernelCmdline("")
		require.NoError(t, err)
		assert.Empty(t, out)
	})
}

func TestDamonMonitorRegion(t *testing.T) {
	const gib = uint64(1) << 30
	cases := []struct {
		name      string
		arch      string
		mem       uint64
		wantStart uint64
		wantEnd   uint64
		wantOK    bool
	}{
		{"amd64 zero", "amd64", 0, 0, 0, false},
		{"amd64 2G", "amd64", 2 * gib, 4096, 2 * gib, true},
		{"amd64 3G", "amd64", 3 * gib, 4096, 3 * gib, true},
		{"amd64 4G", "amd64", 4 * gib, 4096, 5 * gib, true},
		{"amd64 255G", "amd64", 255 * gib, 4096, 256 * gib, true},
		{"amd64 256G", "amd64", 256 * gib, 4096, 257 * gib, true},
		{"arm64 zero", "arm64", 0, 0, 0, false},
		{"arm64 1G", "arm64", 1 * gib, 1 * gib, 2 * gib, true},
		{"arm64 2G", "arm64", 2 * gib, 1 * gib, 3 * gib, true},
		// 0xbc00_0000 (2.9375 GiB) is the largest RAM size that fits below
		// MEM_32BIT_RESERVED_START; one byte more spills to 4 GiB.
		{"arm64 hole edge", "arm64", 0xbc00_0000, 1 * gib, 0xfc00_0000, true},
		{"arm64 hole +1", "arm64", 0xbc00_0000 + 1, 1 * gib, 0x1_0000_0000 + 1, true},
		{"arm64 4G", "arm64", 4 * gib, 1 * gib, 5*gib + gib/16, true},
		{"arm64 255G", "arm64", 255 * gib, 1 * gib, 256*gib + gib/16, true},
		{"unsupported arch", "s390x", 4 * gib, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := damonMonitorRegion(tc.arch, tc.mem)
			assert.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				return
			}
			assert.Equal(t, tc.wantStart, start)
			assert.Equal(t, tc.wantEnd, end)
		})
	}
}

func TestEnsureDamonMonitorRegion(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("monitor region formula is arch-specific; covered on amd64")
	}
	const mib = uint64(1) << 20

	t.Run("append when absent", func(t *testing.T) {
		out, err := ensureDamonMonitorRegion(`["damon_reclaim.enabled=Y"]`, 4096*mib)
		require.NoError(t, err)
		var params []string
		require.NoError(t, json.Unmarshal([]byte(out), &params))
		assert.Equal(t, []string{
			"damon_reclaim.enabled=Y",
			"damon_reclaim.monitor_region_start=4096",
			"damon_reclaim.monitor_region_end=5368709120",
		}, params)
	})

	t.Run("keep explicit non-zero region", func(t *testing.T) {
		in := `["damon_reclaim.monitor_region_start=1048576","damon_reclaim.monitor_region_end=3221225471"]`
		out, err := ensureDamonMonitorRegion(in, 4096*mib)
		require.NoError(t, err)
		assert.JSONEq(t, in, out)
	})

	t.Run("reject end below computed start", func(t *testing.T) {
		_, err := ensureDamonMonitorRegion(
			`["damon_reclaim.enabled=Y","damon_reclaim.monitor_region_end=1024"]`, 4096*mib)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be less than")
	})

	t.Run("reject explicit start above computed end", func(t *testing.T) {
		_, err := ensureDamonMonitorRegion(
			`["damon_reclaim.monitor_region_start=1099511627776"]`, 4096*mib)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be less than")
	})

	t.Run("reject unparseable explicit bound", func(t *testing.T) {
		_, err := ensureDamonMonitorRegion(
			`["damon_reclaim.monitor_region_end=abc"]`, 4096*mib)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a decimal or 0x-prefixed integer")
	})

	t.Run("replace explicit zero region", func(t *testing.T) {
		out, err := ensureDamonMonitorRegion(
			`["damon_reclaim.monitor_region_start=0","damon_reclaim.monitor_region_end=0"]`, 2048*mib)
		require.NoError(t, err)
		var params []string
		require.NoError(t, json.Unmarshal([]byte(out), &params))
		assert.Equal(t, []string{
			"damon_reclaim.monitor_region_start=4096",
			"damon_reclaim.monitor_region_end=2147483648", // 2G <= 3G, end = mem
		}, params)
	})

	t.Run("no region leaves zero placeholders intact", func(t *testing.T) {
		in := `["damon_reclaim.monitor_region_start=0","damon_reclaim.monitor_region_end=0"]`
		out, err := ensureDamonMonitorRegion(in, 0)
		require.NoError(t, err)
		assert.JSONEq(t, in, out)
	})

	t.Run("treat 00, 0x0 and empty as zero placeholders", func(t *testing.T) {
		for _, v := range []string{"0", "00", "0x0", ""} {
			in := fmt.Sprintf(
				`["damon_reclaim.monitor_region_start=%s","damon_reclaim.monitor_region_end=%s"]`, v, v)
			out, err := ensureDamonMonitorRegion(in, 4096*mib)
			require.NoError(t, err)
			var params []string
			require.NoError(t, json.Unmarshal([]byte(out), &params))
			assert.Equal(t, []string{
				"damon_reclaim.monitor_region_start=4096",
				"damon_reclaim.monitor_region_end=5368709120",
			}, params, "value %q", v)
		}
	})

	t.Run("fill missing end when only start given", func(t *testing.T) {
		out, err := ensureDamonMonitorRegion(
			`["damon_reclaim.enabled=Y","damon_reclaim.monitor_region_start=1048576"]`, 4096*mib)
		require.NoError(t, err)
		var params []string
		require.NoError(t, json.Unmarshal([]byte(out), &params))
		assert.Contains(t, params, "damon_reclaim.monitor_region_start=1048576")
		assert.Contains(t, params, "damon_reclaim.monitor_region_end=5368709120")
	})

	t.Run("no damon param leaves list untouched", func(t *testing.T) {
		in := `["page_reporting.page_reporting_order=6"]`
		out, err := ensureDamonMonitorRegion(in, 4096*mib)
		require.NoError(t, err)
		assert.JSONEq(t, in, out)
	})

	t.Run("empty input", func(t *testing.T) {
		out, err := ensureDamonMonitorRegion("", 4096*mib)
		require.NoError(t, err)
		assert.Empty(t, out)
	})
}

func TestSumContainerMemory(t *testing.T) {
	t.Run("nil resources skipped", func(t *testing.T) {
		total, err := sumContainerMemory(nil)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), total)
		total, err = sumContainerMemory([]*types.Container{nil, {}})
		require.NoError(t, err)
		assert.Equal(t, uint64(0), total)
	})

	t.Run("single container", func(t *testing.T) {
		containers := []*types.Container{
			{Resources: &types.Resource{Mem: "4096Mi"}},
		}
		total, err := sumContainerMemory(containers)
		require.NoError(t, err)
		assert.Equal(t, uint64(4096)<<20, total)
	})

	t.Run("multiple containers summed", func(t *testing.T) {
		containers := []*types.Container{
			{Resources: &types.Resource{Mem: "2048Mi"}},
			{Resources: &types.Resource{Mem: "2Gi"}},
		}
		total, err := sumContainerMemory(containers)
		require.NoError(t, err)
		assert.Equal(t, uint64(4)<<30, total)
	})

	t.Run("malformed memory is an error", func(t *testing.T) {
		containers := []*types.Container{
			{Resources: &types.Resource{Mem: "4096Mi"}},
			{Resources: &types.Resource{Mem: "not-a-quantity"}},
		}
		_, err := sumContainerMemory(containers)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not-a-quantity")
	})

	t.Run("zero memory contributes zero", func(t *testing.T) {
		total, err := sumContainerMemory([]*types.Container{
			{Resources: &types.Resource{Mem: "4096Mi"}},
			{Resources: &types.Resource{Mem: "0"}},
		})
		require.NoError(t, err)
		assert.Equal(t, uint64(4096)<<20, total)
	})

	t.Run("empty and negative memory are errors", func(t *testing.T) {
		for _, mem := range []string{"", "-1Gi"} {
			_, err := sumContainerMemory([]*types.Container{{Resources: &types.Resource{Mem: mem}}})
			require.Error(t, err, "mem %q", mem)
		}
	})
}

func TestContainerNeedsDamonRegion(t *testing.T) {
	key := constants.CubeAnnotationVMKernelCmdlineAppend

	assert.False(t, containerNeedsDamonRegion(nil))
	assert.False(t, containerNeedsDamonRegion([]*types.Container{}))
	assert.False(t, containerNeedsDamonRegion([]*types.Container{nil}))
	assert.False(t, containerNeedsDamonRegion([]*types.Container{{}}))
	assert.False(t, containerNeedsDamonRegion([]*types.Container{{Annotations: map[string]string{}}}))

	// Only the first container's annotation reaches the sandbox spec, so an
	// annotation on a later container must not trigger the auto-fill.
	assert.False(t, containerNeedsDamonRegion([]*types.Container{
		{},
		{Annotations: map[string]string{key: `["damon_reclaim.enabled=Y"]`}},
	}))

	assert.True(t, containerNeedsDamonRegion([]*types.Container{
		{Annotations: map[string]string{key: `["damon_reclaim.enabled=Y"]`}},
	}))

	// page_reporting.* does not need a DAMON region, so it must not trigger the
	// memory sum (which rejects resources.mem "0").
	assert.False(t, containerNeedsDamonRegion([]*types.Container{
		{Annotations: map[string]string{key: `["page_reporting.page_reporting_order=6"]`}},
	}))

	// Malformed input is reported by FilterVMKernelCmdline, not here.
	assert.False(t, containerNeedsDamonRegion([]*types.Container{
		{Annotations: map[string]string{key: `not-json`}},
	}))
}

func TestAutoFillRegionFor(t *testing.T) {
	// An empty InstanceType is the cubebox default, matching injectEnvdSidecar.
	assert.True(t, autoFillRegionFor(""))
	assert.True(t, autoFillRegionFor(cubebox.InstanceType_cubebox.String()))
	assert.False(t, autoFillRegionFor("other"))
}

func TestCheckAndGetContainerAnnotationFirstOnly(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("monitor region formula is arch-specific; covered on amd64")
	}
	raw := `["damon_reclaim.enabled=Y"]`

	t.Run("first container fills region", func(t *testing.T) {
		c := &cubebox.ContainerConfig{Annotations: map[string]string{}}
		cnt := &types.Container{
			Resources:   &types.Resource{Mem: "4096Mi"},
			Annotations: map[string]string{constants.CubeAnnotationVMKernelCmdlineAppend: raw},
		}
		require.NoError(t, checkAndGetContainerAnnotation(c, cnt, uint64(4096)<<20, true, true))
		assert.Contains(t, c.Annotations[constants.CubeAnnotationVMKernelCmdlineAppend],
			"damon_reclaim.monitor_region_end=5368709120")
	})

	t.Run("non-first container rejected", func(t *testing.T) {
		c := &cubebox.ContainerConfig{Annotations: map[string]string{}}
		cnt := &types.Container{
			Resources:   &types.Resource{Mem: "4096Mi"},
			Annotations: map[string]string{constants.CubeAnnotationVMKernelCmdlineAppend: raw},
		}
		err := checkAndGetContainerAnnotation(c, cnt, uint64(4096)<<20, true, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be set on the first container")
	})
}

func TestCheckAndGetContainerAnnotationNonCubeboxSkipsAutoFill(t *testing.T) {
	raw := `["damon_reclaim.enabled=Y"]`
	c := &cubebox.ContainerConfig{Annotations: map[string]string{}}
	cnt := &types.Container{
		Resources:   &types.Resource{Mem: "4096Mi"},
		Annotations: map[string]string{constants.CubeAnnotationVMKernelCmdlineAppend: raw},
	}
	// autoFillRegion is false for non-cubebox instance types: the region must
	// be supplied by the caller, so the annotation is kept verbatim.
	require.NoError(t, checkAndGetContainerAnnotation(c, cnt, uint64(4096)<<20, false, true))
	assert.JSONEq(t, raw, c.Annotations[constants.CubeAnnotationVMKernelCmdlineAppend])
}

func TestCheckAndGetAnnotationRejectsSandboxLevel(t *testing.T) {
	req := &types.CreateCubeSandboxReq{
		Annotations: map[string]string{
			constants.CubeAnnotationVMKernelCmdlineAppend: `["damon_reclaim.enabled=Y"]`,
		},
	}
	err := checkAndGetAnnotation(req, &cubebox.RunCubeSandboxRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be set on the first container")
}

package hardware

import (
	"context"
	"testing"
)

// Captured from `mpv --audio-device=help` on the dev box (mpv 0.41).
const sampleAudioHelp = `List of detected audio devices:
  'auto' (Autoselect device)
  'pipewire' (Default (pipewire))
  'pipewire/alsa_output.pci-0000_0b_00.1.hdmi-stereo' (GA102 Digital Stereo (HDMI))
  'alsa' (Default (alsa))
  'alsa/hdmi:CARD=NVidia,DEV=0' (HDA NVidia, DELL U2414H/HDMI Audio Output)
`

const sampleVOHelp = `Available video outputs:
  gpu-next         Video output based on libplacebo
  gpu              Shader-based GPU Renderer
  dmabuf-wayland   Wayland dmabuf video output
  null             Null video output
`

const sampleGPUAPIHelp = `Available GPU APIs:
  auto
  vulkan
  opengl
  vulkan

Available GPU APIs and contexts:
  auto auto
  vulkan waylandvk
  opengl drm
`

const sampleVersion = `mpv v0.41.0 Copyright © 2000-2025 mpv/MPlayer/mplayer2 projects
 built on Feb 11 2026 22:07:06`

func TestParseAudioDevices(t *testing.T) {
	got := ParseAudioDevices(sampleAudioHelp)
	if len(got) != 5 {
		t.Fatalf("got %d devices, want 5: %+v", len(got), got)
	}
	if got[0].Name != "auto" || got[0].Description != "Autoselect device" {
		t.Errorf("device[0] = %+v", got[0])
	}
	last := got[len(got)-1]
	if last.Name != "alsa/hdmi:CARD=NVidia,DEV=0" {
		t.Errorf("last device name = %q", last.Name)
	}
	if last.Description != "HDA NVidia, DELL U2414H/HDMI Audio Output" {
		t.Errorf("last device desc = %q", last.Description)
	}
}

func TestParseVOList(t *testing.T) {
	got := ParseVOList(sampleVOHelp)
	want := []string{"gpu-next", "gpu", "dmabuf-wayland", "null"}
	if len(got) != len(want) {
		t.Fatalf("vo list = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("vo[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseGPUAPIs(t *testing.T) {
	got := ParseGPUAPIs(sampleGPUAPIHelp)
	// Deduped, contexts table excluded.
	want := []string{"auto", "vulkan", "opengl"}
	if len(got) != len(want) {
		t.Fatalf("gpu apis = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("gpu_api[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseVersion(t *testing.T) {
	if got := ParseVersion(sampleVersion); got != "0.41" {
		t.Errorf("ParseVersion = %q, want 0.41", got)
	}
	if got := ParseVersion("garbage\nno banner"); got != "" {
		t.Errorf("ParseVersion(garbage) = %q, want empty", got)
	}
}

func TestEnumerateMissingBinaryNoError(t *testing.T) {
	// A binary that does not exist must not 500; empty lists + a note.
	rep := Enumerate(context.Background(), "definitely-not-mpv-xyz", t.TempDir())
	if rep.Note == "" {
		t.Error("expected a note when mpv binary is missing")
	}
	if rep.AudioDevices == nil || rep.DRMConnectors == nil || rep.Screens == nil {
		t.Error("lists must be non-nil even when mpv is missing")
	}
	if len(rep.AudioDevices) != 0 {
		t.Error("no audio devices expected without mpv")
	}
}
